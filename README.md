## Compile / Run
1 - install golang from golang.org for your platform
2 - download repo
3 - run `go build .`
4 - have `chdman` from the MAME project installed somewhere in your path.

Try it in a test folder first. Program will unzip any .zip files, and convert the ISO or CUE/BIN files in it to CHD using chdman. 

I am not responsible for any loss of data. Use at your own risk. 

===[Enhancements]===

Did you know that whenever you download a file using Microsoft Edge (or, AFAIK, any Chrome-based browser), it adds a hidden NTFS record with the URL from which the file was downloaded?

This info can really come in handy if you go nuts downloading legacy ROM files, then end up with hundreds of files with opaque names whose purpose isn't necessarily obvious. The problem is, if you convert the .zip file to .chd, that potentially helpful metadata goes up in smoke.

My enhancements to the original program (which ultimately ended up dwarfing the original in size) add the following fields as a Zone.Identifier ADS:

[ZoneTransfer]
ZoneId=3
HostUrl=(redacted)
zipfile-Name=(redacted).zip
zipfile-Size=3088620327
zipfile-SHA256=408498af7b2519dc821f95292a83e62407f6afdd49e44de3b1b90cba4db575db
zipfile-CreationDate=2026-03-20 02:16:24 -0400 EDT
zipfile-LastModified=2026-03-19 02:19:55 -0400 EDT
processedFile=(redacted).iso
processedFile-Size=4180148222
processedFile-SHA256=a38f71b7a02f1ae0fe65cc78137ee9c943b75b44473ae1cd5ed89ef0e61400e3
processedFile-CreationDate=2026-03-20 02:25:09 -0400 EDT
processedFile-LastModified=1996-12-24 18:32:00 -0500 EST
chd-CreationDate=2026-03-20 02:25:30 -0400 EDT

The names are probably self-explanatory, but just in case...
* ZoneId=3 means it was downloaded from the internet. For now, it's faked to 3, but I really ought to use the number from the original .zip file
* zipfile-name: the name of the original zip or .7z file
* zipfile-size: size of the zip/7z file in bytes
* zipfile-SHA256: SHA2-256 hash of the original zip/7z file
* zipfile-CreationDate and zipfile-LastModified. Self-evident.
* processedFile: the name of the file within the zip/7z file that was actually USED to create the .chd
* processedFile-Size: its length in bytes
* processedFile-CreationDate and processedFile-LastModified: timestamps of the aforementioned processedFile, as indicated by metadata within the original archive.
* chd-CreationDate: the timestamp when this program ran to create it.

