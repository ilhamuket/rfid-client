#!/usr/bin/env python3
"""
RFID Raw Frame Debugger — baudrate 57600 confirmed
Dump frame, extract EPC 12-byte (24 hex char) dengan benar.
"""

import serial
import binascii
import time
import sys

PORT     = "COM15"
BAUDRATE = 57600
READ_LEN = 128   # lebih besar supaya dapat frame utuh

RESET  = "\033[0m"
GRAY   = "\033[90m"
YELLOW = "\033[33m"
GREEN  = "\033[32m"
CYAN   = "\033[36m"
BOLD   = "\033[1m"

EPC_PREFIXES = ["E280", "E200", "E007"]

def find_epcs(raw_hex: str) -> list[tuple[int, str]]:
    found = []
    for prefix in EPC_PREFIXES:
        idx = 0
        while True:
            pos = raw_hex.find(prefix, idx)
            if pos < 0:
                break
            candidate = raw_hex[pos:pos+24]
            if len(candidate) == 24:
                found.append((pos, candidate))
            idx = pos + 1
    return found

def hexdump(data: bytes) -> str:
    lines = []
    for i in range(0, len(data), 16):
        chunk = data[i:i+16]
        hex_part = " ".join(f"{b:02X}" for b in chunk)
        asc_part = "".join(chr(b) if 32 <= b < 127 else "." for b in chunk)
        lines.append(f"  {i:04x}: {hex_part:<47}  {asc_part}")
    return "\n".join(lines)

def now():
    return time.strftime("%H:%M:%S")

print(f"\n{BOLD}{CYAN}=== RFID DEBUGGER (57600 baud) ==={RESET}")
print(f"  Port: {PORT}  Baud: {BAUDRATE}\n")

try:
    ser = serial.Serial(PORT, BAUDRATE, timeout=1)
    ser.close(); ser.open()
    print(f"{GREEN}✅ Terhubung{RESET}\n")
except Exception as e:
    print(f"\033[31m✗ {e}{RESET}"); sys.exit(1)

frame_count = 0
last_seen   = {}

try:
    while True:
        data = ser.read(READ_LEN)
        if not data:
            continue

        raw = binascii.hexlify(data).decode().upper()

        # debounce
        if raw in last_seen and time.time() - last_seen[raw] < 1.0:
            continue
        last_seen[raw] = time.time()

        frame_count += 1
        epcs = find_epcs(raw)

        print(f"[{now()}] Frame #{frame_count}  ({len(data)} bytes)")
        print(hexdump(data))
        print(f"  raw: {raw}")

        if epcs:
            for pos, epc in epcs:
                print(f"  {GREEN}{BOLD}✓ EPC @ index {pos}: {epc}{RESET}")
        else:
            print(f"  {YELLOW}? EPC tidak ditemukan{RESET}")
        print()

except KeyboardInterrupt:
    print(f"\n{GRAY}Selesai.{RESET}")
finally:
    ser.close()