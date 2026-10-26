# Implementation checkpoint

The approved architecture and baseline are inputs. This owner implements the coupled schema, readers, analytics, and UI sequentially. No child agents are started. Independent review belongs to the parent.

Each source has one worker and a bounded refresh queue. SQLite transactions cover persistence only. Native credentials are read once per probe. The monitor never renews them.

Verification units are domain and fixtures, durable collection and live readers, then authenticated HTTP and UI. Each unit ends in passing focused checks and a commit. Final checks cover race detection, the full existing suite, both executable builds, and the actual handler with deterministic history outside the production data root.
