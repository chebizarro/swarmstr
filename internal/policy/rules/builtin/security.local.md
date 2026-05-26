---
id: security-block-secret-echo
name: Block secret echoing
action: block
event_types: [bash, tool]
message: Do not echo API keys or private keys into logs or shell history.
conditions:
  - field: command
    regex: '(?i)echo .*?(api[_-]?key|private[_-]?key|secret|token)'
---
Blocks commands that visibly echo secret-like variables or literals.
