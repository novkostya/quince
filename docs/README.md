# docs/

How quince is built and why. These files are written for people working on quince, not for
people running it — [`../deploy/`](../deploy/) is where that lives.

| | |
| --- | --- |
| [`quince.design.md`](quince.design.md) | Architecture: components, job state machine, storage, security model |
| [`quince.stack.md`](quince.stack.md) | Technology decisions, and the alternatives that were considered |
| [`contracts.md`](contracts.md) | The frozen interfaces — REST, WebSocket, vault RPC, cache rules |
| [`ui.design.md`](ui.design.md) | Frontend conventions and visual direction |
| [`specs/`](specs/) | One spec per unit of work |

**Historical references here — `qn.N` and lettered decision ids — resolve in the
[devlog](https://github.com/novkostya/quince-devlog), and are kept rather than scrubbed, so the
record stays traceable.** They are deliberately absent from anything a user reads: the README,
`deploy/`'s operator-facing docs, and text the product prints.
