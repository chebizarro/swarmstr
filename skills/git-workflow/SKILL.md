---
name: git-workflow
description: "Advanced git operations: rebase strategies, cherry-pick workflows, bisect automation, worktree management, conflict resolution, branch cleanup. Use when: (1) complex git operations beyond basic add/commit/push, (2) resolving merge conflicts, (3) reorganizing commit history, (4) managing multiple working branches. NOT for: basic git (use git tools directly), GitHub API operations (use github skill)."
when_to_use: "Use when the user needs help with complex git operations, conflict resolution, or branch management."
user-invocable: true
disable-model-invocation: true
---

# Git Workflow

Advanced git operations done safely with recovery points.

## Goal
Execute complex git operations without losing work.

## Safety Rules
1. **Always check `git_status` first.** Stash or commit dirty work before destructive operations.
2. **Create a backup branch** before rebase or reset: `git branch backup/before-rebase`
3. **Never force-push to shared branches** without explicit user confirmation.

## Common Workflows

### Interactive Rebase (squash/reorder/edit)
```bash
# Check what you're working with
git log --oneline -10

# Rebase last N commits
git rebase -i HEAD~N

# Squash: change 'pick' to 'squash' (or 's') for commits to merge
# Reorder: move lines up/down
# Edit: change 'pick' to 'edit' to modify a commit
```

### Cherry-Pick
```bash
# Pick specific commits onto current branch
git cherry-pick <commit-hash>

# Pick a range
git cherry-pick <oldest>^..<newest>

# If conflicts: resolve, then
git cherry-pick --continue
```

### Bisect (find the commit that broke something)
```bash
git bisect start
git bisect bad HEAD          # current is broken
git bisect good <known-good> # this commit was fine

# Git checks out middle commit. Test it, then:
git bisect good  # or
git bisect bad

# Repeat until found. Then:
git bisect reset
```

### Worktrees (parallel branches without stashing)
```bash
# Create a worktree for a branch
git worktree add ../feature-branch feature-branch

# List active worktrees
git worktree list

# Clean up
git worktree remove ../feature-branch
```

### Conflict Resolution
1. Check which files conflict: `git_status`
2. Read the conflicted file — look for `<<<<<<<`, `=======`, `>>>>>>>`
3. Decide: keep ours, theirs, or merge both
4. Use `file_edit` to resolve each conflict
5. `git add <file>` and `git rebase --continue` (or `git merge --continue`)

### Branch Cleanup
```bash
# Delete merged local branches
git branch --merged main | grep -v main | xargs git branch -d

# Prune remote tracking branches
git fetch --prune

# Find stale branches (no commits in 30 days)
git for-each-ref --sort=-committerdate --format='%(refname:short) %(committerdate:relative)' refs/heads/
```

### Undo Operations
```bash
# Undo last commit (keep changes staged)
git reset --soft HEAD~1

# Undo last commit (keep changes unstaged)
git reset HEAD~1

# Completely undo last commit (discard changes)
git reset --hard HEAD~1

# Undo a pushed commit (safe, creates new commit)
git revert <commit-hash>
```

## Guardrails
- Always confirm before force-push or hard reset.
- Prefer `git revert` over `git reset` for pushed commits.
- Use `git reflog` to recover from mistakes.
- When in doubt, create a backup branch first.
