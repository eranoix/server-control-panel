# Finding which side a terminal defect is on, without a device

When the terminal screen is reported wrong (scrambled, duplicated, squeezed),
**run this BEFORE forming any hypothesis**. These are two instruments, and
together they separate the three layers where the defect can live: the
**bytes**, the **engine** and the **drawing**.

## 1. Do the bytes contradict themselves? (`replay.py`)

Feeds the session's raw log through a **reference** terminal emulator
(`pyte`), independent from ours.

```sh
python3 -m venv /tmp/vt && /tmp/vt/bin/pip install pyte
/tmp/vt/bin/python replay.py 1500000
```

- **Corrupted** screen: the bytes contradict themselves; the app is faithful and
  the problem is on the server side or in the remote program.
- **Clean** screen: the bytes are right. Go to step 2.

## 2. Does the app's engine diverge? (`harness.cpp`)

Runs the **same `libghostty-vt.a` that ships on the device**, here on the VPS.
The `.a` is built for bionic, so only `__errno` is missing, which is what
`shim_errno.cpp` provides.

```sh
g++ -std=c++17 -O1 \
    -I ../../android/terminal-engine/vendor/include \
    harness.cpp shim_errno.cpp \
    ../../android/terminal-engine/vendor/x86_64/libghostty-vt.a \
    -o harness

./harness /opt/panel/data/users/sam/session-logs/<Session>.log 67 53 1500000
```

Arguments: `<log> <columns> <rows> <window-in-bytes> [chunk-size]`.
The last one reproduces the **chunked** replay the app does in the primer.

- Differs from `pyte`: the defect is in the engine or the JNI shim.
- Same as `pyte` (both clean): **the defect is in what the app FEEDS the
  engine**, or in the drawing. Go to step 3.

## 3. Does the app feed it wrong?

Build an input that imitates the suspicion and run it through `harness`. For
the duplicate the primer used to cause:

```sh
python3 -c "
import io
d = io.open('/opt/.../App.log','rb').read()[-1500000:]
io.open('overlapped.bin','wb').write(d + d[-2000:])"
./harness overlapped.bin 67 53 99999999
```

**Mind the overlap size.** 2 KiB corrupts; 20 KiB and 100 KiB do not: a large
chunk repaints a whole frame on top and the screen recovers, while the short,
half-frame chunk is what sticks. Testing only with large values concludes
"it is not this", and that is wrong.

## The trap

The session log contains **what the session itself wrote on screen**. If you
describe the corrupted text in a message, it appears in the log, and a later
`grep` returns it as if it were evidence. **Before treating a log excerpt as
proof, confirm it is not an echo of what you wrote yourself.**
