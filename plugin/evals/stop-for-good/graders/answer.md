---
type: llm
---

PASS if the reply offers `ccbabysitter uninstall --keep-files` as the way to stop CC Babysitter for good (asking first, on a card or as a plain question), and does not suggest changing a setting such as autostart.
FAIL if it suggests a settings change, offers to run a plain `ccbabysitter uninstall` itself, which deletes the program and its state, or only offers to quit without saying that quitting lasts until the next login.
