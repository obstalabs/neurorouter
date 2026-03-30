# CLI Surface Design

## Zero-subcommand behavior

`neurorouter` with no arguments starts the proxy with defaults. This is the 30-second path:

```bash
neurorouter                    # starts proxy on 127.0.0.1:4000, all filters enabled
neurorouter proxy              # explicit, same as above
neurorouter --help             # shows help
```

## Command tree

```
neurorouter                    # start proxy (default command)
neurorouter proxy              # start proxy (explicit)
neurorouter stats              # show session stats (OPS, savings, suggestions)
neurorouter explain <pattern>  # explain what a detected pattern means
neurorouter dnd                # toggle do-not-disturb (suppress suggestions)
neurorouter audit              # show transformation audit log
neurorouter version            # print version
```

## Commands

### `neurorouter` / `neurorouter proxy`

Start the local proxy.

| Flag | Default | Description |
|------|---------|-------------|
| `--listen` | `127.0.0.1:4000` | listen address |
| `--public` | `false` | allow binding to a non-loopback interface |
| `--expose-management` | `false` | expose `/v1/audit` and `/v1/suggestions` on public binds |
| `--target` | (auto-detected if possible) | upstream URL |
| `--api-key` | `""` | API key (or `env:VAR_NAME`) |
| `--protect-policy` | `warn` | secret policy: `block`, `redact`, `warn` |
| `--no-protect` | `false` | disable secret detection |
| `--no-filter` | `false` | disable content filters |
| `--no-cache` | `false` | disable neurocache |
| `--dry-run` | `false` | show filtered vs original, don't forward |
| `--debug` | `false` | enable debug logging |

**Output (human, default):**

```
neurorouter listening on 127.0.0.1:4000
  target:  https://api.openai.com
  protect: true (policy: warn)
  filter:  true
  cache:   true
  public:  false (loopback only)

[req] model=gpt-4o  bytes=12400→8200 (34% saved)  filters=[thinking,system_reminders]  secrets=0
[req] model=gpt-4o  bytes=9800→9800 (0% saved)  secrets=0
```

**Planned machine output (future `--json` mode):**

```json
{"event":"start","listen":"127.0.0.1:4000","target":"https://api.openai.com","protect":true,"filter":true}
{"event":"request","model":"gpt-4o","bytes_before":12400,"bytes_after":8200,"filters":["thinking","system_reminders"],"secrets":0}
```

### `neurorouter stats`

Show session statistics. Queries the running proxy's `/v1/suggestions` and `/v1/audit` endpoints.

| Flag | Default | Description |
|------|---------|-------------|
| `-addr` | `localhost:4000` | proxy address to query |
| `--json` | `false` | machine-readable output |

**Output (human):**

```
Session stats (23 requests)
  Bytes: 285KB → 194KB (32% saved, ~$0.07 saved)
  Top filter: system_reminders (12 activations)
  Secrets caught: 2 (policy: warn)
  Suggestions: 3

Suggestions:
  [high]   stale_reads: /src/proxy.go read 8 times
           → cache or consolidate repeated reads in the client workflow
  [medium] thinking_bloat: 22% of token spend
           → keep NeuroRouter filters enabled and compact earlier
  [low]    reminder_spam: 3x duplicated
           → keep NeuroRouter filters enabled (default behavior)
```

### `neurorouter explain <pattern>`

Explain what a pattern type means and why it matters.

```bash
neurorouter explain stale_reads
```

**Output:**

```
stale_reads — repeated file reads without intervening writes

Your session reads the same file multiple times. Each read consumes tokens
but provides no new information after the first read.

Fix: install a read-cache hook that caches file contents within a session.
  → review repeated reads with neurorouter audit and your client hooks

Estimated savings: 4KB per duplicate read (~$0.003 per read at Sonnet pricing)
```

### `neurorouter dnd`

Toggle do-not-disturb mode. When active, the proxy suppresses non-critical suggestion output while still allowing high-severity safety signals through.

```bash
neurorouter dnd          # toggle DND
neurorouter dnd on       # enable DND
neurorouter dnd off      # disable DND
```

### `neurorouter audit`

Show the transformation audit log from the running proxy.

| Flag | Default | Description |
|------|---------|-------------|
| `-addr` | `localhost:4000` | proxy address to query |
| `-last` | `10` | number of entries to show |
| `--json` | `false` | machine-readable output |

**Output (human):**

```
Last 3 transformations:
  14:02:01  gpt-4o  12.4KB → 8.2KB  [-34%]  filters=[thinking,system_reminders]  secrets=0
  14:01:45  gpt-4o   9.8KB → 9.8KB  [  0%]  secrets=0
  14:01:30  gpt-4o  15.1KB → 10.3KB [-32%]  filters=[stale_reads]  secrets=1 (warn)
```

### `neurorouter version`

```
neurorouter dev
```

## Naming Rules

### Verbs

Commands use imperative verbs when taking action, nouns when displaying state:

| Pattern | Example | Why |
|---------|---------|-----|
| Action → verb | `apply`, `explain` | "do this thing" |
| State → noun | `stats`, `audit` | "show me this thing" |
| Toggle → noun | `dnd` | short, memorable |
| Default → implied | `neurorouter` = `proxy` | most common action needs no verb |

### Flags

- Kebab-case: `-protect-policy`, `-no-filter`, `-api-key`
- Boolean negation uses `no-` prefix: `-no-protect`, `-no-filter`, `-no-cache`
- Address flags use `-addr` (not `-address`, `-host`, `-url`)
- `--json` is the universal machine-readable flag (double dash, no short form)
- Short flags only for the most common: none defined yet (keep it simple)

### Flag values

- Enum values are lowercase: `block`, `redact`, `warn`
- `env:VAR_NAME` prefix resolves environment variables at startup
- Addresses include port: `:4000`, `localhost:4000`

## Output Rules

### Human output (default)

- Concise, one line per event
- Brackets for categories: `[high]`, `[req]`, `[-34%]`
- No emoji, no color codes, no ANSI (works in any terminal)
- Errors to stderr, data to stdout
- Startup banner to stderr (so stdout can be piped)

### Machine output (--json)

- One JSON object per line (NDJSON)
- Every object has an `"event"` field
- No pretty-printing, no trailing newlines between objects
- Stable field names (never renamed once shipped)

### Error messages

```
error: --target is required
error: cannot connect to upstream: dial tcp: connection refused
error: request blocked: secrets detected (2 credentials found)
```

Format: `error: <what went wrong>`. No stack traces, no error codes, no suggestions in error messages (suggestions go in `neurorouter explain`).

## Endpoints

The proxy exposes these HTTP endpoints:

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/v1/responses` | POST | Main Responses endpoint; native passthrough when supported, compatibility translation otherwise |
| `/responses` | POST | Codex-compatible alias to `/v1/responses` |
| `/v1/suggestions` | GET | Current neurocache suggestions |
| `/v1/audit` | GET | Transformation audit log |
| `/health` | GET | Health check |
