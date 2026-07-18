"""Does open/close of the CP2102 reset the ESP8266?  (Answer: yes, every time.)

This is the regression guard for the single fact the whole host design rests on:
EVERY open of the serial port reboots the board, so a short-lived process must
never open it directly. That is why `tools/claude-usage` uses a long-lived holder
process that opens the port once and takes lines over a unix socket instead.

Mimics what a short-lived statusline binary would do: open -> write a line ->
close, repeatedly. The firmware prints "usage_display <ver>" from setup(), so
seeing that banner after an open proves the board rebooted.

Run A: plain open/close (dtr=False, rts=False).
Run B: same, but with HUPCL cleared first, so DTR/RTS don't drop on close --
       the theory being the reset comes from the line *transition*, not the open.
       It does not help; both runs reboot 5/5.

Manual hardware probe, not part of `go test`. Needs pyserial:
    python3 -m venv /tmp/probe && /tmp/probe/bin/pip install pyserial
    /tmp/probe/bin/python firmware/scripts/reset_probe.py
Stop the holder first -- only one process should own the port:
    pkill -x claude-usage
"""
import subprocess, sys, time
import serial

PORT = "/dev/ttyUSB0"
BANNER = b"usage_display"
LINE = b"@U|five=42|seven=7|sub=Max 20x|five_s=3600|seven_s=172800\n"


def cycle(n, settle=1.2):
    """One open -> write -> read -> close cycle. True if the board rebooted."""
    ser = serial.Serial()
    ser.port = PORT
    ser.baudrate = 115200
    ser.timeout = 0.2
    ser.dtr = False
    ser.rts = False
    ser.open()
    ser.write(LINE)
    ser.flush()
    got = b""
    deadline = time.time() + settle
    while time.time() < deadline:
        got += ser.read(256)
    ser.close()
    rebooted = BANNER in got
    print(f"  cycle {n}: {'REBOOTED' if rebooted else 'no reset'}"
          f"  ({len(got):>4} B){'  <- ' + got[:40].decode(errors='replace').strip() if rebooted else ''}")
    return rebooted


def run(label, hupcl):
    print(f"\n=== {label} ===")
    subprocess.run(["stty", "-F", PORT, "hupcl" if hupcl else "-hupcl"], check=True)
    print(f"  (hupcl {'set' if hupcl else 'cleared'})")
    resets = sum(cycle(i) for i in range(1, 6))
    print(f"  -> {resets}/5 cycles rebooted the board")
    return resets


a = run("Run A: default (hupcl on) - today's behaviour", hupcl=True)
b = run("Run B: hupcl cleared - DTR/RTS held across close", hupcl=False)

print("\n" + "=" * 58)
print(f"Run A (default):      {a}/5 reboots")
print(f"Run B (-hupcl):       {b}/5 reboots")
print("=" * 58)
if b == 0 and a > 0:
    print("VERDICT: -hupcl fixes it. Statusline design is VIABLE.")
elif a == 0 and b == 0:
    print("VERDICT: open/close never resets. Design VIABLE with no workaround.")
else:
    print("VERDICT: board still resets on open. Statusline design NOT viable as-is;")
    print("         needs a port-holding helper process.")
