---
id: bd-workflow-no-markdown-todos
name: Use bd for task tracking
action: warn
event_types: [file]
message: This repo uses bd/beads for task tracking; do not add markdown TODO task lists.
conditions:
  - field: content
    regex: '(?m)^\s*- \[[ xX]\] '
---
Warns on markdown task lists that duplicate bd issue tracking.
