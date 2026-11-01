#!/bin/sh
# Builds KotelAlchemika.exe and KotelAlchemika-Setup.exe (Windows, amd64). Needs Go 1.24+ and Python 3.
set -e
cd "$(dirname "$0")"
cp web/index.html app/index.html
(cd app && go mod tidy && GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-H=windowsgui -s -w" -o ../dist/KotelAlchemika.exe .)
python3 -c "
import gzip,shutil
with open('dist/KotelAlchemika.exe','rb') as a, gzip.open('setup/app.exe.gz','wb',9) as b: shutil.copyfileobj(a,b)"
(cd setup && go mod tidy && GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-H=windowsgui -s -w" -o ../dist/KotelAlchemika-Setup.exe .)
ls -la dist
