@echo off
rem Builds KotelAlchemika.exe and KotelAlchemika-Setup.exe on Windows. Needs Go 1.24+ and Python 3.
cd /d "%~dp0"
copy /y web\index.html app\index.html >nul
pushd app
go mod tidy || exit /b 1
go build -trimpath -ldflags "-H=windowsgui -s -w" -o ..\dist\KotelAlchemika.exe . || exit /b 1
popd
python -c "import gzip,shutil; shutil.copyfileobj(open('dist/KotelAlchemika.exe','rb'), gzip.open('setup/app.exe.gz','wb',9))" || exit /b 1
pushd setup
go mod tidy || exit /b 1
go build -trimpath -ldflags "-H=windowsgui -s -w" -o ..\dist\KotelAlchemika-Setup.exe . || exit /b 1
popd
dir dist
