"""quince vault sidecar package.

Today this ships the CLI floor (``--version`` + ``selftest``); the JSON-RPC ``serve``
loop (contracts.md §4) and the decryption logic land in qn.8. The seam is deliberately
language-neutral so a future all-Go vault can replace this package (D4).
"""

__version__ = "0.0.0-dev"
