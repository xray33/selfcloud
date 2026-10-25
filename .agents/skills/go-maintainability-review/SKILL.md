---
name: go-maintainability-review
description: >-
  Audits and improves Go code maintainability in SelfCloud by detecting copy-paste
  duplication, high cyclomatic/cognitive complexity, deeply nested control flow,
  and Go clean code anti-patterns. Use this skill whenever the user asks to check,
  review, audit, or refactor Go code for maintainability, clean code, complexity
  reduction, or deduplication.
---

# Go Maintainability Review & Refactoring Skill

This skill provides a fast, deterministic workflow optimized for **Gemini 3.8 Flash** (and other models) to find and fix Go maintainability issues in SelfCloud without introducing regressions or over-abstraction.

---

## Mode Selection

Determine the operating mode from the user's prompt before starting:

1. **Report Mode (Default)**: Used when the user asks to *check*, *find*, *review*, or *audit* Go code for maintainability improvements. Do **not** modify source files; produce a prioritized maintainability report with concrete refactoring sketches.
2. **Refactor Mode**: Used when the user explicitly asks to *fix*, *refactor*, or *apply* maintainability improvements (either across a package or for specific findings). Apply focused refactorings and verify with `make lint` and `make test`.

---

## Step-by-Step Workflow

### Phase 1: Run the Static Maintainability Scan

Always begin by running the helper script to gather complexity, duplication, and clean-code diagnostics in a single pass:

```bash
./.agents/skills/go-maintainability-review/scripts/scan_maintainability.sh [optional_package_path...]
```

- By default, the script scans `./internal/...` and `./cmd/...` (excluding `_test.go` to keep signal high) using [golangci-maintainability.yml](./resources/golangci-maintainability.yml).
- Pass `--include-tests` if the user also wants test files analyzed.
- Do **not** manually grep or read every Go file in the repository; rely on the scan output to pinpoint exact file and line hotspots.

### Phase 2: Targeted Code Inspection & Checklist Review

1. Read [go-clean-code-checklist.md](./references/go-clean-code-checklist.md) to load the refactoring rules, Go 1.26 idioms, and SelfCloud invariants.
2. Select the highest-signal hotspots from the scan output across the three core categories:
   - **Copy-Paste / Duplication (`dupl`, `goconst`)**: Repeated repository query blocks, duplicated handler utilities (e.g., identical base URL resolution or error responses across handlers), or repeated validation blocks.
   - **Cyclomatic & Cognitive Complexity (`gocyclo > 15`, `gocognit > 20`, `nestif >= 4`, `funlen`)**: Monolithic CLI dispatchers (`cmdUser`, `cmdServe`), deep `if/else` trees in HTTP/WebDAV handlers or auth middleware (`DualAuthMiddleware`, `GoogleCallbackHandler`, `UploadHandler`, `handleCopyDir`, `ReindexUser`).
   - **Go Clean Code Practices (`gocritic`, `revive`, `errorlint`, `unparam`, `perfsprint`)**: `os.Exit` after `defer` (`exitAfterDefer`), unused internal helper parameters, package import shadowing (`storage`), `if-else` chains replaceable by `switch`, and `fmt.Errorf` without format verbs.
3. Use `read_file` with targeted `StartLine` and `EndLine` ranges around the flagged functions to inspect their actual control flow and domain context.

### Phase 3: Filter Out False Positives (Anti-Noise Gate)

Before recommending or applying a change, filter out low-value churn:
- **Do not** merge functions that merely look similar if their domain or SQL semantics will diverge.
- **Do not** replace readable `goqu.Ex{"user_id": userID}` column names with global string constants just to silence `goconst`. (However, **do** investigate when `goconst` flags repeated URLs/schemes like `https` or `localhost:8080` across multiple handlers, which indicates a copy-pasted helper function).
- **Do not** alter standard `http.HandlerFunc` signatures (`w http.ResponseWriter, r *http.Request`) when `r` is unused; focus `unparam` fixes on internal helper functions.
- **Never** break SelfCloud invariants defined in `AGENTS.md` (audit logging order via `audit.Record(ctx, ...)`, `goqu` node update fields, symlink & path length checks).

### Phase 4A: Output Format (Report Mode)

Group findings into three clear sections prioritized by maintainability impact (**High → Medium → Low**). Use clickable `file://` links for all files and symbols:

1. **Copy-Paste & Code Duplication Opportunities**
   - Location(s) with line ranges (`file:///.../path.go#L10-L40`).
   - Why it is duplicated and the exact helper signature to extract.
2. **Cyclomatic & Cognitive Complexity Hotspots**
   - Function link + current metrics (e.g., `gocyclo: 68`, `gocognit: 125`, `lines: 146`).
   - Root cause of complexity (e.g., inline subcommand switch, deeply nested `if/else`, mixed parsing and storage logic).
   - Concrete decomposition plan (which helper functions or guard clauses to extract).
3. **Go Clean Code & Idiom Improvements**
   - Grouped summary of actionable clean-code fixes (`exitAfterDefer`, unused helper parameters, import shadowing, early returns, modern Go idioms).

### Phase 4B: Execution & Verification (Refactor Mode)

When applying refactorings:
1. Refactor **one logical unit at a time** (e.g., deduplicate one repository or decompose one complex handler/CLI function).
2. Re-run the maintainability scan on the modified package to confirm complexity/duplication dropped:
   ```bash
   ./.agents/skills/go-maintainability-review/scripts/scan_maintainability.sh ./internal/<pkg>/...
   ```
3. Run the project's standard linter and test suite to verify zero regressions:
   ```bash
   make lint && make test
   ```
