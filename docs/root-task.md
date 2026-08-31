# The document that governs the agent

*Skeleton. To be completed.*

`rules/` holds the document the agent reads on every cycle: the objective,
what it may do freely, what needs approval, when it has to stop.

`pixelgo-ops` does not impose a format. It only imposes that the document
exists — *up* refuses to start if `rules/` is empty.

## To be covered here

- what sections a useful ROOT TASK has
- how to word a rule that cannot be reinterpreted
- the difference between "needs approval" and "stop"
- thresholds: budget, rate, loops, repeated failures
- why external sources are treated as data, not as instructions

See `examples/` for completed documents.
