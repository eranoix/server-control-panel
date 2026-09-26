"""Replays the session's RAW bytes through a REFERENCE terminal emulator.

If the screen comes out corrupted here, the bytes contradict themselves and the
app is innocent. If it comes out clean, the app's emulator is what diverges.
"""
import sys

import pyte

LOG = "/opt/panel/data/users/sam/session-logs/App.log"
COLS, ROWS = 67, 53
WINDOW = int(sys.argv[1]) if len(sys.argv) > 1 else 1_500_000

data = open(LOG, "rb").read()[-WINDOW:]

screen = pyte.Screen(COLS, ROWS)
stream = pyte.Stream(screen)
stream.feed(data.decode("utf-8", errors="replace"))

print(f"=== REFERENCE emulator (pyte), {COLS}x{ROWS}, last {len(data)} bytes ===")
for i, line in enumerate(screen.display):
    print(f"{i:02d}|{line.rstrip()}")
