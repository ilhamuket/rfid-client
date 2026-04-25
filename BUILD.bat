@echo off
chcp 65001 >nul
title Build RFID Scanner

echo.
echo  Building rfid-scanner.exe...
go build -o rfid-scanner.exe .
if errorlevel 1 (
    echo  GAGAL build! Cek error di atas.
    pause
    exit
)
echo  ✓ Build sukses!

echo  Membuat paket distribusi...
for /f "delims=" %%i in ('powershell -Command "[Environment]::GetFolderPath('Desktop')"') do set DESKTOP=%%i
set DEST=%DESKTOP%\rfid-gate

if exist "%DEST%" rmdir /s /q "%DEST%"
mkdir "%DEST%"
copy rfid-scanner.exe "%DEST%\"
copy SETUP.bat "%DEST%\"

echo.
echo  ════════════════════════════════════════
echo  ✓ Selesai! Folder rfid-gate di Desktop.
echo  ════════════════════════════════════════
echo.

explorer "%DEST%"
pause