---
name: security-audit
description: "Audit code for security vulnerabilities: injection, secrets, auth bypass, insecure deps. Use when: (1) reviewing code for security, (2) user asks about security of a change, (3) checking for leaked secrets, (4) assessing new API endpoints. NOT for: general code review (use code-review), performance issues (use perf-profile)."
when_to_use: "Use when the user asks about security, wants a security review, or when reviewing code that handles auth, user input, or secrets."
user-invocable: true
disable-model-invocation: false
---

# Security Audit

Find vulnerabilities that matter. Skip theoretical risks with no exploit path.

## Goal
Identify concrete security issues in the code with clear exploitation scenarios and fixes.

## Workflow

1. **Identify attack surface.** What takes user input? What touches auth/authz? What handles secrets?
2. **Scan for vulnerability classes** (checklist below).
3. **Verify findings.** Trace the data flow to confirm the vulnerability is reachable.
4. **Report** with severity, exploit scenario, and fix.

## Vulnerability Checklist

### Injection
- SQL injection: string concatenation in queries → use parameterized queries
- Command injection: user input in `exec`, `system`, `os.Command` → use arg arrays
- Path traversal: user input in file paths without sanitization → validate and canonicalize
- XSS: unescaped user content in HTML → use template auto-escaping
- Template injection: user input in template strings → never pass user data as template source

### Secrets & Credentials
- Hardcoded API keys, tokens, passwords in source
- Secrets in logs, error messages, or stack traces
- `.env` or credential files committed to git
- Secrets passed via URL query parameters (logged by proxies)

### Authentication & Authorization
- Missing auth checks on endpoints
- Broken access control (IDOR — can user A access user B's data?)
- JWT issues: no expiry, weak signing, algorithm confusion
- Session fixation or missing session invalidation

### Cryptography
- Weak algorithms (MD5, SHA1 for security, DES, RC4)
- Hardcoded or predictable IVs/nonces
- Broken random: `math/rand` instead of `crypto/rand` for security
- Missing TLS verification (InsecureSkipVerify)

### Data Handling
- Sensitive data in plaintext (PII, passwords stored unhashed)
- Missing rate limiting on auth endpoints
- SSRF: fetching user-provided URLs without validation
- Deserialization of untrusted data

### Dependencies
- Known CVEs in dependencies (`npm audit`, `go vuln check`, `pip-audit`)
- Outdated dependencies with security patches available
- Transitive dependencies with known issues

## Output Format

```
## Security Audit: [scope]

### Critical (exploit likely)
- [CWE-XXX] [file:line] Description. Exploit: how an attacker would use this. Fix: what to change.

### High (exploit possible)
- [file:line] Description.

### Medium (defense-in-depth)
- [file:line] Description.

### Summary
N critical, N high, N medium. Dependencies checked: yes/no.
```

## Guardrails
- Only report issues you can trace to a real code path.
- State the CWE number when applicable.
- Prioritize issues by exploitability, not theoretical severity.
- If you can't confirm a finding, say "potential" and explain what's missing.
