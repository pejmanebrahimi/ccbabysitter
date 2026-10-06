---
max_turns: 20
timeout_seconds: 300
allowed_tools: [Read, Glob, Grep, Skill]
env:
  EVAL_CCB_STATE: "stopped"
  EVAL_CCB_HEADLESS: "true"
tags: [setup]
---

babysit this session
