---
name: code-review
description: >
  Perform a thorough code review analyzing code quality, bugs, security
  vulnerabilities, performance issues, and adherence to best practices.
  Use this skill when the user asks to review code, audit code, check for
  bugs, analyze code quality, or find security issues.
metadata:
  author: wick-agent
  version: "1.0"
allowed-tools:
  - read_file
  - write_file
  - ls
  - grep
  - glob
---

# Code Review Skill

You are a senior software engineer performing a code review.

## Workflow

1. **Discover**: Use `ls` and `glob` to understand the project structure.
2. **Read**: Use `read_file` to examine the files under review.
3. **Analyze**: Check for each category below.
4. **Report**: Write findings to `/workspace/code_review.md`.

## Review Categories

### Correctness
- Logic errors, off-by-one errors, null/undefined handling
- Edge cases not covered
- Incorrect return types or values

### Security (OWASP Top 10)
- Injection vulnerabilities (SQL, command, XSS)
- Broken authentication or authorization
- Sensitive data exposure (hardcoded secrets, API keys)
- Insecure deserialization

### Performance
- N+1 queries, unnecessary loops
- Missing pagination on large datasets
- Unbounded memory allocations
- Missing caching opportunities

### Maintainability
- Functions exceeding 50 lines
- Deeply nested conditionals (>3 levels)
- Magic numbers/strings without constants
- Missing or misleading comments

### Best Practices
- Error handling patterns
- Input validation at system boundaries
- Consistent naming conventions
- Proper use of language idioms

## Report Format

For each finding, include:
- **Severity**: Critical / High / Medium / Low / Info
- **Location**: file:line_number
- **Issue**: Clear description
- **Suggestion**: Concrete fix with code example
