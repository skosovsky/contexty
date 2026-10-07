#!/usr/bin/env python3
"""Run real consumer fixtures in an isolated module, never persist local replaces."""
import argparse
import os
from pathlib import Path
import shutil
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parent.parent


def run(args, cwd, env):
    subprocess.run(args, cwd=cwd, env=env, check=True)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--published", nargs="?", const="latest")
    parser.add_argument("--peer", type=Path)
    parser.add_argument("--baseline", action="store_true")
    args = parser.parse_args()
    if args.baseline and not args.published:
        parser.error("baseline requires --published")
    env = dict(os.environ, GOWORK="off")
    with tempfile.TemporaryDirectory(prefix="contexty-chat-") as temporary:
        checkout = Path(temporary)
        shutil.copytree(ROOT / "integration/chat", checkout, dirs_exist_ok=True)
        if args.baseline:
            for file in checkout.rglob("*.go"):
                file.unlink()
            shutil.copy(ROOT / "integration/baseline_test.go.txt", checkout / "baseline_test.go")
        if args.published:
            run(["go", "get", "github.com/skosovsky/contexty@" + args.published], checkout, env)
        else:
            run(["go", "mod", "edit", "-replace=github.com/skosovsky/contexty=" + str(ROOT)], checkout, env)
            if args.peer:
                run(["go", "mod", "edit", "-replace=github.com/skosovsky/prompty=" + str(args.peer.resolve())], checkout, env)
        run(["go", "mod", "tidy"], checkout, env)
        run(["go", "list", "-m", "all"], checkout, env)
        run(["go", "test", "-race", "-count=1", "./..."], checkout, env)
        run(["go", "vet", "./..."], checkout, env)
        if not args.baseline:
            run(["go", "run", "./cmd/recipe"], checkout, env)
        print("Published baseline only; new role contract NOT checked" if args.baseline else "Full consumer semantic fixtures PASS", flush=True)


if __name__ == "__main__":
    main()
