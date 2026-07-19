"""qn.0 vault gate: the CLI floor works and the decryption stack imports on this host."""

from __future__ import annotations

import pytest

from quince_vault import __version__
from quince_vault.__main__ import _selftest, main


def test_version_flag(capsys: pytest.CaptureFixture[str]) -> None:
    with pytest.raises(SystemExit) as exc:
        main(["--version"])
    assert exc.value.code == 0
    assert __version__ in capsys.readouterr().out


def test_selftest_imports_decryption_stack(capsys: pytest.CaptureFixture[str]) -> None:
    # Proves iphone_backup_decrypt + pycryptodome load on this interpreter/libc.
    assert _selftest() == 0
    assert "ok" in capsys.readouterr().out


def test_no_subcommand_is_usage_error() -> None:
    assert main([]) == 2
