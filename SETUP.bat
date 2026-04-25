@echo off
chcp 65001 >nul
title RFID Scanner Setup

cls
echo ╔══════════════════════════════════════╗
echo ║        RFID RACE SCANNER SETUP       ║
echo ╚══════════════════════════════════════╝
echo.

REM ── Cek config.json sudah ada ──────────────────────────────────────────────
if exist config.json (
    echo  Config ditemukan. Langsung jalankan? [Y/N]
    set /p reuse=" → "
    if /i "%reuse%"=="Y" goto RUN
    if /i "%reuse%"=="y" goto RUN
)

REM ── Pilih environment ───────────────────────────────────────────────────────
:ask_env
cls
echo ╔══════════════════════════════════════╗
echo ║        RFID RACE SCANNER SETUP       ║
echo ╚══════════════════════════════════════╝
echo.
echo  [1] PRODUCTION  (https://event-run.com/api/rfid)
echo  [2] LOCAL DEV   (http://localhost:8000/api/rfid)
echo.
set /p envChoice=" Pilih environment (1/2): "

if "%envChoice%"=="1" set endpoint=https://event-run.com/api/rfid
if "%envChoice%"=="2" set endpoint=http://localhost:8000/api/rfid
if not defined endpoint goto ask_env

REM ── Pilih Event ID ──────────────────────────────────────────────────────────
:ask_event
cls
echo ╔══════════════════════════════════════╗
echo ║        RFID RACE SCANNER SETUP       ║
echo ╚══════════════════════════════════════╝
echo.
echo  Pilih Event:
echo.
echo  [1] Bandung Night Run 2025    (ID: 1)
echo  [2] Jakarta Marathon 2025     (ID: 2)
echo  [3] Bali Ultra Trail 2025     (ID: 3)
echo.
set /p eventChoice=" Pilih event (1/2/3): "

if "%eventChoice%"=="1" ( set event_id=1 & set event_name=Scoutrun 2026 )

if not defined event_id goto ask_event

REM ── Pilih checkpoint type ───────────────────────────────────────────────────
:ask_checkpoint
cls
echo ╔══════════════════════════════════════╗
echo ║        RFID RACE SCANNER SETUP       ║
echo ╚══════════════════════════════════════╝
echo.
echo  [1] start
echo  [2] finish
echo  [3] checkpoint
echo.
set /p cpChoice=" Pilih tipe gate (1/2/3): "

if "%cpChoice%"=="1" set checkpoint_type=start
if "%cpChoice%"=="2" set checkpoint_type=finish
if "%cpChoice%"=="3" set checkpoint_type=checkpoint
if not defined checkpoint_type goto ask_checkpoint

REM ── List COM ports ──────────────────────────────────────────────────────────
:ask_port
cls
echo ╔══════════════════════════════════════╗
echo ║        RFID RACE SCANNER SETUP       ║
echo ╚══════════════════════════════════════╝
echo.
echo  COM port tersedia:
echo  ─────────────────
mode | findstr "COM"
echo.
set /p port=" Masukkan COM port (contoh: COM3): "
if not defined port goto ask_port

REM ── Device Key ──────────────────────────────────────────────────────────────
:ask_key
cls
echo ╔══════════════════════════════════════╗
echo ║        RFID RACE SCANNER SETUP       ║
echo ╚══════════════════════════════════════╝
echo.
set /p device_key=" Masukkan device key: "
if not defined device_key goto ask_key

REM ── Konfirmasi ──────────────────────────────────────────────────────────────
cls
echo ╔══════════════════════════════════════╗
echo ║        RFID RACE SCANNER SETUP       ║
echo ╚══════════════════════════════════════╝
echo.
echo  Konfirmasi konfigurasi:
echo  ───────────────────────────────────────
echo  Environment : %endpoint%
echo  Event       : %event_name% (ID: %event_id%)
echo  Gate        : %checkpoint_type%
echo  COM Port    : %port%
echo  ───────────────────────────────────────
echo.
set /p confirm=" Simpan dan jalankan? [Y/N]: "
if /i "%confirm%"=="N" goto ask_env
if /i "%confirm%"=="n" goto ask_env

REM ── Tulis config.json ───────────────────────────────────────────────────────
echo { > config.json
echo   "port": "%port%", >> config.json
echo   "baudrate": 115200, >> config.json
echo   "endpoint": "%endpoint%", >> config.json
echo   "device_key": "%device_key%", >> config.json
echo   "event_id": %event_id%, >> config.json
echo   "checkpoint_type": "%checkpoint_type%", >> config.json
echo   "debounce_ms": 3000, >> config.json
echo   "retry_queue_max": 200 >> config.json
echo } >> config.json

echo.
echo  ✓ config.json tersimpan!
echo.
timeout /t 2 /nobreak >nul

REM ── Jalankan scanner dengan auto-restart ────────────────────────────────────
:RUN
cls
echo ╔══════════════════════════════════════╗
echo ║        RFID RACE SCANNER RUNNING     ║
echo ╚══════════════════════════════════════╝
echo.
echo  Environment : %endpoint%
echo  Event       : %event_name% (ID: %event_id%)
echo  Gate        : %checkpoint_type%
echo  Port        : %port%
echo.
echo  ════════════════════════════════════════
echo  Scanner aktif. Jangan tutup window ini.
echo  ════════════════════════════════════════
echo.

:loop
rfid-scanner.exe
echo.
echo  Scanner berhenti — restart otomatis dalam 3 detik...
timeout /t 3 /nobreak >nul
goto loop