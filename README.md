# aur

An AUR helper

## Installation
```bash
git clone https://github.com/mahdisarikhani/aur.git
cd aur
go build
install -Dm755 aur /usr/bin/aur
```

Add the following lines to your `/etc/pacman.conf`
```conf
[aur]
SigLevel = Optional TrustAll
Server = file:///home/user/.cache/aur/
```
