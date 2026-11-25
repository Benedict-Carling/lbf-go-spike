"""Reads `go test -json` on stdin: saves it, prints the run as `go test -v` would, then the slowest tests and any failures."""

import json
import os
import re
import sys
from datetime import datetime

SHOWN = ("=== RUN", "--- ", "ok", "FAIL", "PASS")
TOP = 15


def when(stamp):
    return datetime.fromisoformat(re.sub(r"(\.\d{6})\d+", r"\1", stamp.replace("Z", "+00:00")))


def table(rows, unit_header):
    lines = [f"| {unit_header} | Test |", "|---:|---|"]
    lines += [f"| {secs:.1f}s | `{name}` |" for secs, name in rows]
    return "\n".join(lines)


def main():
    save_to, title = sys.argv[1], sys.argv[2] if len(sys.argv) > 2 else "Conformance"
    sys.stdout.reconfigure(encoding="utf-8", errors="replace")
    started, wall, elapsed, output, failed = {}, {}, {}, {}, []
    with open(save_to, "w", encoding="utf-8") as saved:
        for raw in sys.stdin.buffer:
            line = raw.decode("utf-8", "replace")
            saved.write(line)
            try:
                event = json.loads(line)
            except ValueError:
                sys.stdout.write(line)
                continue
            test, action = event.get("Test"), event.get("Action")
            if action == "output":
                text = event.get("Output", "")
                if test:
                    output.setdefault(test, []).append(text)
                if text.startswith(SHOWN):
                    sys.stdout.write(text)
                    sys.stdout.flush()
            elif test and action == "run":
                started[test] = when(event["Time"])
            elif test and action in ("pass", "fail", "skip"):
                if test in started:
                    wall[test] = (when(event["Time"]) - started[test]).total_seconds()
                elapsed[test] = event.get("Elapsed", 0.0)
                if action == "fail":
                    failed.append(test)

    top = sorted(((s, n) for n, s in wall.items() if "/" not in n), reverse=True)[:TOP]
    cases = sorted(((s, n) for n, s in elapsed.items() if "/" in n), reverse=True)[:TOP]
    total = sum(s for n, s in wall.items() if "/" not in n)

    print(f"\n{title}: {len(wall)} tests and cases, {total:.0f}s of top-level test time")
    print("Slowest tests, start to finish including their parallel cases:")
    for secs, name in top:
        print(f"  {secs:7.1f}s  {name}")
    print("Slowest cases:")
    for secs, name in cases:
        print(f"  {secs:7.1f}s  {name}")
    for name in failed:
        print(f"\n--- output of failed {name}")
        sys.stdout.write("".join(output.get(name, [])))

    summary = os.environ.get("GITHUB_STEP_SUMMARY")
    if summary:
        with open(summary, "a", encoding="utf-8") as out:
            out.write(f"### {title}\n\n")
            if failed:
                out.write("Failed: " + ", ".join(f"`{n}`" for n in failed) + "\n\n")
            out.write(f"<details><summary>Slowest tests ({total:.0f}s top-level)</summary>\n\n{table(top, 'Wall')}\n\n")
            out.write(f"Slowest cases\n\n{table(cases, 'Time')}\n\n</details>\n\n")


if __name__ == "__main__":
    main()
