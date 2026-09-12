# Router Retry-After wait (`NABD_ROUTER_RETRY_AFTER_WAIT`)

## Why

In fallback mode the router walks every configured route once and then reports
exhaustion. When the first route answers `429` with a short `Retry-After` and
the remaining routes fail for unrelated reasons, the whole run dies even though
waiting a few seconds would have succeeded:

```
run_error: all 2 route(s) exhausted (mixed failures); shortest retry-after: 20s
  [groq:openai/gpt-oss-120b]: Rate limit reached ... Please try again in 19.4625s.
  [nvidia:deepseek-ai/deepseek-v4-pro-0813]: prestream timeout
```

## Behavior

`NABD_ROUTER_RETRY_AFTER_WAIT` is an integer number of seconds in `0..120`.
The default is `0`, which disables the feature and keeps the historical
fail-fast behavior exactly. An out-of-range or non-numeric value is a startup
error, never a silently substituted default.

When the value is positive and the first full route cycle ends in exhaustion:

1. The router computes the shortest positive `Retry-After` across the failed
   attempts.
2. If that delay is within the configured budget, it emits a route trace with
   `status: "waiting"` and the reason `retry-after <d>; retrying all routes once`.
3. It waits through the injected clock with a `select` on the parent context, so
   cancellation and deadlines stay authoritative.
4. It runs **one** more full cycle over all routes, then reports exhaustion if
   that cycle also fails.

The wait happens only before the commit point, so it can never interleave with
output that was already delivered, and it happens at most once per request.

## Latency ceiling

```
cycles × route_count × (NABD_ROUTER_PRESTREAM_TIMEOUT + ROUTE_CLEANUP_TIMEOUT)
  + one wait ≤ NABD_ROUTER_RETRY_AFTER_WAIT
```

with `cycles ≤ 2`.

## Suggested Termux configuration

```
NABD_ROUTER_RETRY_AFTER_WAIT=25
NABD_ROUTER_PRESTREAM_TIMEOUT=12
```

On a low tokens-per-minute tier, also consider putting the less rate-limited
provider first in `NABD_ROUTES`.

## Notes

- v1 key only for now (`~/.ag/config` or the environment). Config v2 parity
  would add a `router_retry_after_wait` field.
- Security properties are recorded in `docs/THREAT_MODEL.md` under
  "Router timing keys".
