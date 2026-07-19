"""quince-vault command-line entrypoint.

Subcommands (qn.0 floor):
    quince-vault --version      print the vault version
    quince-vault selftest       import the decryption dependency and exit 0 if healthy

`selftest` exists to prove, in CI and in the shipped alpine image, that the OSS
decryption stack (iphone_backup_decrypt + pycryptodome) actually loads on musl — the
single riskiest packaging fact for the Python sidecar (D4).
"""

from __future__ import annotations

import argparse
import sys

from . import __version__


def _selftest() -> int:
    """Import the decryption dependency; return a process exit code."""
    try:
        # Importing the package exercises the whole native stack (pycryptodome).
        import iphone_backup_decrypt  # noqa: F401
        from iphone_backup_decrypt import EncryptedBackup  # noqa: F401
    except Exception as exc:  # pragma: no cover - failure path is the point
        print(f"quince-vault selftest: FAILED to import decryption stack: {exc!r}", file=sys.stderr)
        return 1

    print(f"quince-vault selftest: ok (vault {__version__}, decryption stack importable)")
    return 0


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(prog="quince-vault", description="quince vault sidecar")
    parser.add_argument("--version", action="version", version=__version__)
    sub = parser.add_subparsers(dest="command")
    sub.add_parser("selftest", help="import the decryption dependency; exit 0 if healthy")

    args = parser.parse_args(argv)

    if args.command == "selftest":
        return _selftest()

    parser.print_help(sys.stderr)
    return 2


if __name__ == "__main__":
    raise SystemExit(main())
