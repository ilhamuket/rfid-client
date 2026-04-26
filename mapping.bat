@echo off
chcp 65001 >nul
title RFID Mapping Tool

:start
cls
echo.
echo  ╔══════════════════════════════════════╗
echo  ║       RFID MAPPING TOOL              ║
echo  ╚══════════════════════════════════════╝
echo.

REM Cek apakah mapping.exe sudah ada, kalau belum build dulu
if not exist mapping.exe (
    echo  [BUILD] Kompilasi mapping.go...
    go build -o mapping.exe mapping.go
    if errorlevel 1 (
        echo.
        echo  [ERROR] Build gagal. Pastikan Go terinstall dan dependency lengkap.
        echo  Jalankan: go mod tidy
        pause
        exit /b 1
    )
    echo  [OK] Build selesai.
    echo.
)

REM Jalankan tool
mapping.exe

echo.
echo  ════════════════════════════════════════
echo  Program selesai.
choice /C YN /M "Jalankan lagi?"
if errorlevel 2 goto end
goto start

:end
echo  Sampai jumpa!
timeout /t 2 >nul