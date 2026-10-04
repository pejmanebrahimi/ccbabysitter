package procs

import (
	"strings"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/process"
)

const (
	// tableTTL bounds how often Real rescans every process on the system
	// to rebuild the pid to parent table used for tree walks.
	tableTTL = 3 * time.Second
	// defaultGraceWait is how long Terminate waits for a polite exit
	// before it resorts to killing.
	defaultGraceWait = 3 * time.Second
	// defaultKillWait is how much longer Terminate waits after killing.
	defaultKillWait = 2 * time.Second
	pollInterval    = 20 * time.Millisecond
)

// procInfo is what Real remembers about one process between full table
// rebuilds: just enough to walk the process tree and to answer
// ExeContains without repeating a full system scan for every call.
type procInfo struct {
	ppid int
	exe  string
}

// cpuSample is the last CPU reading taken for a pid, kept so CPUPercent can
// be reported as a delta between two Tree calls rather than a meaningless
// instantaneous reading.
type cpuSample struct {
	createMs int64
	cpuSecs  float64
	at       time.Time
}

// Real reads process facts from the operating system through gopsutil.
type Real struct {
	mu sync.Mutex

	// refreshMu serializes process table rebuilds. It is only ever held
	// around the table rebuild itself, and never together with mu while
	// the rebuild is scanning the operating system, so a slow rebuild
	// never blocks callers that only need the mutex for bookkeeping.
	refreshMu sync.Mutex

	clock func() time.Time

	tableAt time.Time
	table   map[int]procInfo

	samples map[int]cpuSample

	// GraceWait is how long Terminate waits after asking a process to
	// exit politely before it resorts to killing it. KillWait is how
	// much longer it then waits for the kill to take effect. Both have
	// sensible defaults when left zero; tests shrink them to stay fast.
	GraceWait time.Duration
	KillWait  time.Duration
}

// NewReal returns a Real ready to use.
func NewReal() *Real {
	return &Real{
		clock:     time.Now,
		samples:   make(map[int]cpuSample),
		GraceWait: defaultGraceWait,
		KillWait:  defaultKillWait,
	}
}

func (r *Real) now() time.Time {
	if r.clock != nil {
		return r.clock()
	}
	return time.Now()
}

// tableFresh reports the cached table when it exists and is not older than
// tableTTL, so callers can skip a rebuild. It only ever touches r.mu, never
// r.refreshMu, and never does any operating system I/O.
func (r *Real) tableFresh() (map[int]procInfo, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.table != nil && r.now().Sub(r.tableAt) < tableTTL {
		return r.table, true
	}
	return nil, false
}

// buildProcessTable scans every process on the system once. It takes no
// lock: it is meant to run without blocking anything else Real is doing.
func buildProcessTable() map[int]procInfo {
	table := make(map[int]procInfo)
	list, err := process.Processes()
	if err == nil {
		for _, p := range list {
			info := procInfo{}
			if ppid, err := p.Ppid(); err == nil {
				info.ppid = int(ppid)
			}
			if exe, err := p.Exe(); err == nil {
				info.exe = exe
			}
			table[int(p.Pid)] = info
		}
	}
	return table
}

// getTable returns the current pid to parent table, rebuilding it when it
// is stale (or unconditionally when force is true). The rebuild itself
// runs with no lock held, so a slow system-wide scan never blocks Tree,
// Terminate or ExeContains callers that only need a moment of r.mu for
// their own bookkeeping. refreshMu keeps two concurrent rebuilds from
// racing to swap in a table, without ever being held at the same time as
// r.mu during the scan.
func (r *Real) getTable(force bool) map[int]procInfo {
	if !force {
		if t, ok := r.tableFresh(); ok {
			return t
		}
	}

	r.refreshMu.Lock()
	defer r.refreshMu.Unlock()

	if !force {
		// Another goroutine may have just finished a rebuild while this
		// one waited for refreshMu; use it instead of scanning again.
		if t, ok := r.tableFresh(); ok {
			return t
		}
	}

	table := buildProcessTable()

	r.mu.Lock()
	r.table = table
	r.tableAt = r.now()
	for pid := range r.samples {
		if _, ok := table[pid]; !ok {
			delete(r.samples, pid)
		}
	}
	r.mu.Unlock()

	return table
}

// childrenIndex turns a pid to parent table into a parent to children
// table, which is what walking a process tree from its root needs.
func childrenIndex(table map[int]procInfo) map[int][]int {
	idx := make(map[int][]int)
	for pid, info := range table {
		idx[info.ppid] = append(idx[info.ppid], pid)
	}
	return idx
}

// descendant names one process found while walking a process tree, along
// with the parent the table recorded for it at the time of the walk.
type descendant struct {
	pid          int
	expectedPpid int
}

// descendants lists every process descended from root, deepest first: a
// leaf process always appears before the ancestor that spawned it. That
// order is what Terminate needs to end a tree without orphaning anything,
// and it costs nothing extra for Tree, which does not care about order.
// The table can be a few seconds old, so every entry here still needs its
// live parent confirmed before it is trusted; see verifiedChild.
func descendants(idx map[int][]int, root int) []descendant {
	var order []descendant
	visited := map[int]bool{root: true}
	var visit func(pid int)
	visit = func(pid int) {
		for _, child := range idx[pid] {
			if visited[child] {
				continue
			}
			visited[child] = true
			visit(child)
			order = append(order, descendant{pid: child, expectedPpid: pid})
		}
	}
	visit(root)
	return order
}

// verifiedChild returns pid's live process handle only if it is still
// running as a child of expectedPpid. The pid to parent table can be a few
// seconds stale, and pids get reused, so a descendant pid found in it might
// by now belong to a wholly unrelated process; this is the check that
// keeps that unrelated process out of a tree walk or a kill.
func verifiedChild(pid, expectedPpid int) (*process.Process, bool) {
	p, err := process.NewProcess(int32(pid))
	if err != nil {
		return nil, false
	}
	ppid, err := p.Ppid()
	if err != nil || int(ppid) != expectedPpid {
		return nil, false
	}
	return p, true
}

// gone reports whether p is no longer running, treating a zombie (a
// process the operating system has not yet let its parent reap) as gone
// too: it has already exited and will never do anything again, even
// though its pid still shows up as present.
func gone(p *process.Process) bool {
	running, err := p.IsRunning()
	if err != nil || !running {
		return true
	}
	statuses, err := p.Status()
	if err != nil {
		return false
	}
	for _, s := range statuses {
		if s == process.Zombie {
			return true
		}
	}
	return false
}

// member is a live reading of one process. It is always read fresh rather
// than from the cached table, since Tree and Terminate need current facts,
// not facts that might be up to tableTTL old.
type member struct {
	name     string
	rss      uint64
	cpuSecs  float64
	createMs int64
}

func readMember(p *process.Process) (member, bool) {
	ct, err := p.CreateTime()
	if err != nil {
		return member{}, false
	}
	m := member{createMs: ct}
	if name, err := p.Name(); err == nil {
		m.name = name
	}
	if mem, err := p.MemoryInfo(); err == nil && mem != nil {
		m.rss = mem.RSS
	}
	if times, err := p.Times(); err == nil && times != nil {
		m.cpuSecs = times.User + times.System
	}
	return m, true
}

// cpuPercent computes the percentage of one CPU consumed by pid since the
// last sample taken for it, records the new sample, and returns 0 when
// there is no usable previous sample: the first look at a pid, or one whose
// creation time changed because the operating system reused the pid.
func (r *Real) cpuPercent(pid int, createMs int64, cpuSecs float64) float64 {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := r.now()
	percent := 0.0
	if prev, ok := r.samples[pid]; ok && prev.createMs == createMs {
		wall := now.Sub(prev.at).Seconds()
		if wall > 0 {
			delta := cpuSecs - prev.cpuSecs
			if delta < 0 {
				delta = 0
			}
			percent = delta / wall * 100
		}
	}
	if r.samples == nil {
		r.samples = make(map[int]cpuSample)
	}
	r.samples[pid] = cpuSample{createMs: createMs, cpuSecs: cpuSecs, at: now}
	return percent
}

// Alive reports whether pid names a running, non-zombie process whose
// creation time is consistent with procStart.
func (r *Real) Alive(pid int, procStart string) bool {
	p, err := process.NewProcess(int32(pid))
	if err != nil {
		return false
	}
	ct, err := p.CreateTime()
	if err != nil {
		return false
	}
	if gone(p) {
		return false
	}
	return MatchStart(ct, procStart) != Mismatch
}

// CreateTime reports pid's creation time in Unix milliseconds.
func (r *Real) CreateTime(pid int) (int64, bool) {
	p, err := process.NewProcess(int32(pid))
	if err != nil {
		return 0, false
	}
	ct, err := p.CreateTime()
	if err != nil {
		return 0, false
	}
	return ct, true
}

// Parent reports pid's parent process id.
func (r *Real) Parent(pid int) (int, bool) {
	p, err := process.NewProcess(int32(pid))
	if err != nil {
		return 0, false
	}
	ppid, err := p.Ppid()
	if err != nil || ppid <= 0 {
		return 0, false
	}
	return int(ppid), true
}

// Exists reports whether pid is running, and not a zombie, with the given
// creation time.
func (r *Real) Exists(pid int, createMs int64) bool {
	p, err := process.NewProcess(int32(pid))
	if err != nil {
		return false
	}
	if gone(p) {
		return false
	}
	ct, err := p.CreateTime()
	if err != nil {
		return false
	}
	return withinTolerance(ct, createMs, 100) == Matches
}

// looksLikeClaude reads what the operating system says about p and asks
// looksLikeClaudeProgram whether that describes Claude Code itself. It is
// the last resort used to decide whether to touch a process whose identity
// could be neither confirmed nor denied, so it gathers only the program:
// the name, the executable, and the first word of the command line.
func looksLikeClaude(p *process.Process) bool {
	var id programIdentity
	if name, err := p.Name(); err == nil {
		id.Name = name
	}
	if exe, err := p.Exe(); err == nil {
		id.Exe = exe
	}
	if argv, err := p.CmdlineSlice(); err == nil && len(argv) > 0 {
		// Only the program and the argument that can name a script are
		// carried further; everything after them is left behind here.
		if len(argv) > 2 {
			argv = argv[:2]
		}
		id.Argv = argv
	}
	return looksLikeClaudeProgram(id)
}

// waitGone polls until p is gone (exited, or a zombie waiting to be
// reaped) or timeout passes, reporting which happened first.
func waitGone(p *process.Process, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if gone(p) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(pollInterval)
	}
}

// Terminate ends the process tree rooted at pid. A definite mismatch always
// reports success without touching anything, since the pid was never ours
// to end. An unconfirmed identity is only acted on when the process looks
// like a Claude process; otherwise Terminate refuses and leaves it alone.
// When it does proceed, it first confirms the root's identity has not
// changed since the decision was made, then asks every process in the
// tree to exit politely, deepest descendant first and the root last,
// waits for the root to go, and only kills what is still running after
// that wait. Nothing here holds r.mu while signaling or waiting: table
// access is confined to short, already-unlocked helper calls.
//
// "Politely" only means anything on Unix, where the first pass sends
// SIGTERM and a process can save what it is doing before it goes. Windows
// has no such signal: the first pass there already ends the process
// outright, so the grace wait and the kill pass that follow it have
// nothing left to do.
func (r *Real) Terminate(pid int, procStart string) bool {
	root, err := process.NewProcess(int32(pid))
	if err != nil {
		return true
	}
	ct, err := root.CreateTime()
	if err != nil {
		return true
	}

	switch MatchStart(ct, procStart) {
	case Mismatch:
		return true
	case Unknown:
		if !looksLikeClaude(root) {
			return false
		}
	}

	table := r.getTable(true)
	idx := childrenIndex(table)
	kids := descendants(idx, pid)

	members := make([]*process.Process, 0, len(kids)+1)
	for _, d := range kids {
		if p, ok := verifiedChild(d.pid, d.expectedPpid); ok {
			members = append(members, p)
		}
	}

	// The identity decision above and the table rebuild both took time.
	// Re-read the root fresh, immediately before sending anything, and
	// abort untouched if it is no longer the same process: reusing the
	// cached root handle would return the creation time it read at
	// construction, not a current one, so a fresh handle is required.
	recheck, err := process.NewProcess(int32(pid))
	if err != nil {
		return true
	}
	ct2, err := recheck.CreateTime()
	if err != nil || ct2 != ct {
		return true
	}

	members = append(members, root)

	for _, p := range members {
		_ = p.Terminate()
	}

	grace := r.GraceWait
	if grace <= 0 {
		grace = defaultGraceWait
	}
	if !waitGone(root, grace) {
		killWait := r.KillWait
		if killWait <= 0 {
			killWait = defaultKillWait
		}
		for _, p := range members {
			if !gone(p) {
				_ = p.Kill()
			}
		}
		waitGone(root, killWait)
	}

	return gone(root)
}

// Tree reports resource usage for pid and everything descended from it.
func (r *Real) Tree(pid int) (TreeStats, bool) {
	table := r.getTable(false)
	idx := childrenIndex(table)
	kids := descendants(idx, pid)

	rootProc, err := process.NewProcess(int32(pid))
	if err != nil {
		return TreeStats{}, false
	}
	root, ok := readMember(rootProc)
	if !ok {
		return TreeStats{}, false
	}

	var st TreeStats
	st.Uptime = r.now().Sub(time.UnixMilli(root.createMs))
	st.PIDs = append(st.PIDs, pid)
	st.RSS += root.rss
	st.CPUPercent += r.cpuPercent(pid, root.createMs, root.cpuSecs)

	for _, d := range kids {
		p, ok := verifiedChild(d.pid, d.expectedPpid)
		if !ok {
			continue
		}
		m, ok := readMember(p)
		if !ok {
			continue
		}
		st.PIDs = append(st.PIDs, d.pid)
		st.Children = append(st.Children, m.name)
		st.RSS += m.rss
		st.CPUPercent += r.cpuPercent(d.pid, m.createMs, m.cpuSecs)
	}

	st.Processes = len(st.PIDs)
	return st, true
}

// ExeContains reports whether any process on the system has an executable
// path containing substr. The comparison is case-sensitive. It reads from
// the same cached table Tree uses, so it can be as stale as tableTTL.
// Exe is the program of the process with this pid and creation time, and
// ok is false when that exact process is not alive or its program cannot
// be read.
func (r *Real) Exe(pid int, createMs int64) (string, bool) {
	if !r.Exists(pid, createMs) {
		return "", false
	}
	info, ok := r.getTable(true)[pid]
	if !ok || info.exe == "" {
		return "", false
	}
	return info.exe, true
}

func (r *Real) ExeContains(substr string) bool {
	table := r.getTable(false)

	for _, info := range table {
		if strings.Contains(info.exe, substr) {
			return true
		}
	}
	return false
}
