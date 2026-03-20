package main

import (
	"archive/zip"
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/bodgit/sevenzip"
	"github.com/schollz/progressbar/v3"
	"golang.org/x/sys/windows"
)

//https://go.dev/play/p/nE3HLTvMu3v

const DBGADS bool = false // used to suppress error messages related to the reading and writing of NTFS alternate data streams

type ADSResult struct {
	Supported bool // volume supports named streams
	Written   bool // Zone.Identifier was successfully written
}

func replaceExt(path string, newExt string) (newName string) {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	ext := filepath.Ext(path)
	if ext != "" {
		newName = filepath.Join(dir, strings.Replace(base, ext, newExt, 1))
	}
	return
}

func decompressZIP(zipFile string) bool {

	dst := replaceExt(zipFile, "")
	archive, err := zip.OpenReader(zipFile)
	if err != nil {
		log.Error("Can't open ZIP file", "file", zipFile, "error", err)
		if err == zip.ErrFormat {
			slog.Error("Renamed bad ZIP file", "file", zipFile+".bad")
			os.Rename(zipFile, zipFile+".bad")
			return false
		}

	}
	defer archive.Close()

	filesInZip := []string{}
	dirsInZip := []bool{}

	singleEmbeddedDirOrNone := false
	var dirNameIfEmbedded string

	for _, f := range archive.File {
		filesInZip = append(filesInZip, f.Name)
		log.Info("dirs", "dirname", f.Name, "bool", f.FileHeader.FileInfo().IsDir())
		if f.FileHeader.FileInfo().IsDir() {
			dirNameIfEmbedded = f.Name
			dirsInZip = append(dirsInZip, f.FileHeader.FileInfo().IsDir())
			log.Info("IsDir", "dir", dirNameIfEmbedded)
		}
	}
	log.Info("dirsInZip", "dircount", len(dirsInZip), "dirsInZip", dirsInZip)
	singleEmbeddedDirOrNone = len(dirsInZip) == 1 || len(dirsInZip) == 0

	log.Info("singleEmbeddedDirOrNone", "bool", singleEmbeddedDirOrNone, "dirname", dirNameIfEmbedded)

	checkForCUEISO := checkIfCueOrIso(filesInZip)
	if !checkForCUEISO {
		log.Info("No CUE or ISO files found", "file", zipFile)
		return false
	}

	log.Info("checkForCUEISO", "check", checkForCUEISO)
	log.Info("Unzipping ZIP", "count", len(filesInZip), "file", zipFile)
	bar := progressbar.Default(int64(len(filesInZip)))
	var filePath string

	if err := os.MkdirAll(dst, os.ModePerm); err != nil {
		log.Error("Can't make directory as ZIP name", "dir", dst)
		panic(err)
	}

	for _, f := range archive.File {
		bar.Add(1)
		if !f.FileInfo().IsDir() {
			base := filepath.Base(f.Name)
			filePath = filepath.Join(dst, base)
			log.Info("Just file", "file", filePath)
		}

		// if !strings.HasPrefix(filePath, filepath.Clean(dst)+string(os.PathSeparator)) {
		// 	log.Error("invalid file path", "dir", filePath)
		// 	return false
		// }

		if f.FileInfo().IsDir() {
			log.Info("Not creating dir from arch", "dirname", f.Name)
			// if err := os.MkdirAll(filePath, os.ModePerm); err != nil {
			// 	log.Error("Can't make dir for ZIP", "dir", filePath)
			// }
			continue
		}

		log.Info("** filepath.Dir", "value", filepath.Dir(filePath))

		// if err := os.MkdirAll(filepath.Dir(filePath), os.ModePerm); err != nil {
		// 	log.Error("Can't make dir for ZIP", "dir", filePath)
		// 	panic(err)
		// }

		dstFile, err := os.OpenFile(filePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err != nil {
			log.Info("Open file", "name", dstFile, "file", filePath)
			panic(err)
		}

		// close the newly-extracted file
		fileInArchive, err := f.Open()
		if err != nil {
			panic(err)
		}
		if _, err := io.Copy(dstFile, fileInArchive); err != nil {
			panic(err)
		}
		if err := dstFile.Close(); err != nil {
			panic(err)
		}
		if err := fileInArchive.Close(); err != nil {
			panic(err)
		}

		// set the last-modified time of the file we just extracted to match what's in the archive
		modTime := f.Modified
		if modTime.IsZero() {
			modTime = f.ModTime()
		}
		if !modTime.IsZero() {
			if err := os.Chtimes(filePath, time.Now(), modTime); err != nil {
				log.Error("Can't set extracted file mtime", "file", filePath, "error", err)
			} else {
				log.Info("modtime set", modTime)
			}
		} else {
			log.Error("modTime.IsZero()")
		}
	}
	log.Info("returning from func")
	return true
}

func checkIfCueOrIso(filesInZip []string) bool {
	hasCUE := slices.ContainsFunc(filesInZip, func(s string) bool {
		return strings.HasSuffix(strings.ToLower(s), ".cue")
	})

	hasISO := slices.ContainsFunc(filesInZip, func(s string) bool {
		return strings.HasSuffix(strings.ToLower(s), ".iso")
	})

	return (hasCUE || hasISO)
}

func decompress7z(zipFile string) bool {

	dst := replaceExt(zipFile, "")
	archive, err := sevenzip.OpenReader(zipFile)
	if err != nil {
		log.Error("Can't open 7z file", "file", zipFile, "error", err)
		slog.Error("Renamed bad 7z file", "file", zipFile+".bad")
		os.Rename(zipFile, zipFile+".bad")
		return false
	}
	defer archive.Close()

	// log.Info("Unzipping", "file", zipFile)
	filesInZip := []string{}
	for _, f := range archive.File {
		filesInZip = append(filesInZip, f.Name)
	}

	checkForCUEISO := checkIfCueOrIso(filesInZip)
	if !checkForCUEISO {
		log.Info("No CUE or ISO files found", "file", zipFile)
		return false
	}
	log.Info("checkForCUEISO", "check", checkForCUEISO)

	log.Info("Unzipping 7z", "count", len(filesInZip), "file", zipFile)

	// if err := os.MkdirAll(filepath.Dir(filePath), os.ModePerm); err != nil {
	// 	log.Error("Can't make dir for ZIP", "dir", filePath)
	// 	panic(err)
	// }

	bar := progressbar.Default(int64(len(filesInZip)))
	var wg sync.WaitGroup
	for _, f := range archive.File {

		wg.Add(1)
		go func(f *sevenzip.File) bool {
			defer wg.Done()

			filePath := filepath.Join(dst, f.Name)

			if !strings.HasPrefix(filePath, filepath.Clean(dst)+string(os.PathSeparator)) {
				log.Error("invalid file path", "dir", filePath)
				return false
			}

			if f.FileInfo().IsDir() {
				// if err := os.MkdirAll(filePath, os.ModePerm); err != nil {
				// 	log.Error("Can't make dir for ZIP", "dir", filePath)
				// }
				log.Info("Not making embedded directory", "name", f.FileInfo().IsDir())
				// continue
			}

			// go func() {
			defer bar.Add(1)

			if err := os.MkdirAll(filepath.Dir(filePath), os.ModePerm); err != nil {
				log.Error("Can't make dir for 7z", "dir", filePath)
				panic(err)
			}
			log.Info("** filepath.Dir", "value", filePath)
			dstFile, err := os.OpenFile(filePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
			if err != nil {
				panic(err)
			}

			fileInArchive, err := f.Open()
			if err != nil {
				panic(err)
			}

			if _, err := io.Copy(dstFile, fileInArchive); err != nil {
				panic(err)
			}

			dstFile.Close()
			fileInArchive.Close()
			return true
		}(f)
	}
	wg.Wait()

	return true
}

var log = slog.New(slog.NewTextHandler(os.Stdout, nil))
var chdmanRE = regexp.MustCompile(`Compressing, (\d+\.\d)\% .* \(ratio=(\d+\.\d)\%\)`)
var cueFileRE = regexp.MustCompile(`FILE \"([^/]+)\" BINARY`)

func getRatio(text string) (float32, float32) {
	//Compressing, 86.9% complete... (ratio=25.8%)

	// step[\s]+(\d+)`)
	result := chdmanRE.FindStringSubmatch(text)
	// fmt.Println(result[1], result[2])
	complete, _ := strconv.ParseFloat(result[1], 32)
	ratio, _ := strconv.ParseFloat(result[2], 32)
	return float32(complete), float32(ratio)
}

func main() {
	if err := runCmd(); err != nil {
		fmt.Println(err)
	}
}

func fixCueFileCase(file string) string {
	srcFormat := `FILE "([^/]+)" .*`
	// srcFormat := `(.*)`
	// srcFormat := `FILE "(.*)" BINARY`
	b, err := os.ReadFile(file)
	// can file be opened?
	if err != nil {
		fmt.Print(err)
	}
	// bstr := string(b)
	re := regexp.MustCompile(srcFormat)
	log.Info("cuedump", "contents", string(b))
	// cueFile := re.FindStringSubmatch()
	cueFilename := re.FindStringSubmatch(string(b))
	replacedCue := strings.Replace(string(b), cueFilename[1], strings.ToLower(cueFilename[1]), -1)

	// replacedCue := re.ReplaceAllString(bstr, "$1")
	// newStr := regexp.ReplaceAllString(bstr, "(\\w+) (.*)", "replaced $2")
	fmt.Println("***", cueFilename[1])
	log.Info("cuefile", "file", cueFilename[1], "cue", replacedCue)
	// os.Rename(cueFilename, strings.ToUpper(cueFilename))
	os.WriteFile(file+"2", []byte(replacedCue), 0644)
	log.Info("remove", "file", file)
	os.Remove(file)
	os.Rename(file+"2", file)
	return replacedCue
}

func runCmd() (e error) {
	// getRatio
	e = nil
	entries, err := os.ReadDir("./")
	if err != nil {
		log.Info("Couldn't read dir", "error", err)
		return
	}

	var (
		zips           []string
		convertedToCHD int
	)

	for _, e := range entries {
		if !e.IsDir() && !strings.HasPrefix(e.Name(), "._") && (strings.HasSuffix(e.Name(), ".7z") || strings.HasSuffix(e.Name(), ".zip")) {
			zips = append(zips, e.Name())
		}
	}

	log.Info("** gochd **", "ZIP_count", len(zips), "numprocs", runtime.NumCPU())
	curDir, errCurDir := os.Getwd()
	if errCurDir != nil {
		log.Error("Can't get current dir", "error", errCurDir)
		return
	}

	var srcDir string
	srcDir = curDir

	zipHasCUEorISO := false
	// sourceFormatExt := ""
	for _, zip := range zips {
		if strings.HasSuffix(zip, ".zip") {
			// sourceFormatExt = ".zip"
			zipHasCUEorISO = decompressZIP(zip)
		}
		if strings.HasSuffix(zip, ".7z") {
			// sourceFormatExt = ".7z"
			zipHasCUEorISO = decompress7z(zip)
		}

		if !zipHasCUEorISO {
			continue
		}
		dir := replaceExt(zip, "")
		if err := os.Chdir(dir); err != nil {
			continue
		}

		var files []string
		cueFiles, _ := filepath.Glob("*.cue")
		allFiles, _ := filepath.Glob("*")
		isoFiles, _ := filepath.Glob("*.iso")

		log.Info("all", "files", allFiles)
		log.Info("files", "CUE", cueFiles)

		currDir, _ := os.Getwd()
		log.Info("cwd", "currDir", currDir)
		files2, _ := os.ReadDir(currDir)

		log.Info("ReadDir", "files2", files2)
		log.Info("filesGlobCUE", "files", cueFiles)
		if len(cueFiles) == 0 {
			log.Info("filesGlobISO", "files", isoFiles)

			if len(isoFiles) == 0 {
				log.Error("No CUE or ISO found, removing unzipped dir")
				if errDir := os.RemoveAll(dir); errDir != nil {
					log.Error("Can't remove removing dir", "dir", dir, "error", errDir)
				}
				return
			}
		}

		var fullPath string
		fullPath = srcDir + "\\" + zip

		var processFilename string
		if len(cueFiles) > 0 {
			log.Info("isoFiles > 0", "isofiles[0]", cueFiles[0])

			processFilename = cueFiles[0]
			//var cueFilename string
			//fixCueFileCase(cueFilename)

		} else if len(isoFiles) > 0 {
			log.Info("isoFiles > 0", "isofiles[0]", isoFiles[0])
			processFilename = isoFiles[0]
		}

		commands := []string{
			"chdman",
			"createcd",
			"-np", strconv.Itoa(runtime.NumCPU() - 1),
			"-f", "-i", processFilename,
			"-o", filepath.Join("..", dir+".chd"),
		}
		// currDir2, _ := os.Getwd()

		log.Info("exec", "cmd", commands, "dir", dir)

		if DBGADS {
			log.Info("Looking up HostURL ADS for ", fullPath)
		}
		hostURL, hasHostURL, err := readHostURLFromZoneIdentifier(fullPath)
		if DBGADS {
			if err != nil {
				log.Error("Couldn't read Zone.Identifier", "file", zip, "error", err)
			} else if hasHostURL {
				fmt.Println("HostUrl:", hostURL)
			} else {
				log.Info("No HostUrl found", "file", zip)
			}
		}

		var outputFilePath = filepath.Join(srcDir + "\\" + dir + ".chd")
		zipfileModified, err := getLastModifiedTime(fullPath)
		if DBGADS && (err != nil) {
			fmt.Println("error:", err)
		}

		fi, err := os.Stat(fullPath)
		if err != nil {
			return err
		}
		zipfileSize := fi.Size() // int64, bytes
		if DBGADS {
			fmt.Println("original zipfile size: ", zipfileSize)
		}

		var chdDate string
		chdDate = time.Now().Format("2006-01-02 15:04:05 -0700 MST")
		if DBGADS {
			fmt.Println("current time: ", chdDate)
		}

		zipfileHash, err := fileSHA256(fullPath)

		if DBGADS {
			fmt.Println("SHA256:", zipfileHash)
			fmt.Println("Original zipfile:", fullPath)
			fmt.Println("Final output file:", outputFilePath)
		}

		var processFilenamePath = srcDir + "\\" + dir + "\\" + processFilename
		if DBGADS {
			fmt.Println("File being converted:", processFilenamePath)
		}

		fi, err = os.Stat(processFilenamePath)

		var pFileSize int64

		pFileSize = fi.Size()
		if DBGADS {
			fmt.Println("processed filename size:", pFileSize)
		}

		pFileHash, err := fileSHA256(processFilenamePath)
		if DBGADS {
			fmt.Println("processed filename hash:", pFileHash)
		}

		pFileLastModified, err := getLastModifiedTime(processFilenamePath)
		if DBGADS {
			fmt.Println("pFileDate:", pFileLastModified)
		}

		//Compressing, 86.9% complete... (ratio=25.8%)
		// cmdCtx, cmdDone := context.WithCancel(context.Background())
		cmd := exec.Command(commands[0], commands[1:]...)
		// ptmx, _ := pty.Start(cmd)
		// Make sure to close the pty at the end.
		// defer func() { _ = ptmx.Close() }() // Best effort.

		// stdout, _ := cmd.StdoutPipe()

		// lines := make(chan string)

		var stdBuffer bytes.Buffer
		mw := io.MultiWriter(os.Stdout, &stdBuffer)

		cmd.Stdout = mw
		cmd.Stderr = mw

		errCmd := cmd.Start()

		if errCmd != nil {
			log.Error("Failed to process via CHD", "file", files[0], "error", errCmd)
			return
		}

		cmdWaitErr := cmd.Wait()
		// log.Info("stdout/err", "contents", stdBuffer.String())

		files2, _ = os.ReadDir(".")
		log.Info("ReadDir", "files2", files2)
		files2, _ = os.ReadDir("../chill.chd")
		log.Info("ReadDir", "CHD", files2)

		// cmd.Wait()

		// ctx, _ := context.WithCancel(context.Background())

		// select {
		// case outputx := <-lines:
		// 	// I will do somethign with this!
		// }

		// cmd.Wait()

		// os.Rename(replaceExt(files[0], ".chd"), "../"+replaceExt(files[0], ".chd"))
		if err := os.Chdir(curDir); err != nil {
			log.Error("Can't change back to current dir", "dir", curDir)
			return
		}

		if cmdWaitErr == nil {
			if err := os.Remove(zip); err != nil {
				log.Error("Can't remove ZIP!!", "file", zip)
				return
			}
		}

		if errDir := os.RemoveAll(dir); errDir != nil {
			log.Error("Can't remove expanded dir", "error", errDir)
			return
		}
		log.Info("SUCCESS - Created CHD", "file", dir+".chd")
		convertedToCHD++

		// now, write all these values to the ADS
		writeZoneIdentifier(outputFilePath, hostURL, chdDate, zip, zipfileSize, zipfileHash, zipfileModified, processFilename, pFileSize, pFileHash, pFileLastModified)

	}
	log.Info("Converted CHD files", "count", convertedToCHD, "CHD_Count", convertedToCHD, "ZIP_Count", len(zips))

	return nil
}

func readHostURLFromZoneIdentifier(path string) (value string, found bool, err error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", false, err
	}

	// Optional sanity check
	if _, err := os.Stat(abs); err != nil {
		return "", false, err
	}

	f, err := openADSForRead(abs + ":Zone.Identifier")
	if err != nil {
		if isWinFileNotFound(err) {
			return "", false, nil
		}
		return "", false, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "HostUrl=") {
			v := strings.TrimPrefix(line, "HostUrl=")
			v = strings.TrimRight(v, "\x00")
			v = strings.TrimRight(v, "\r\n")
			v = strings.TrimRight(v, "\n")

			return v, true, nil
		}
	}

	if err := scanner.Err(); err != nil {
		return "", false, err
	}

	return "", false, nil
}

func openADSForRead(path string) (*os.File, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}

	h, err := windows.CreateFile(
		p,
		windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		return nil, err
	}

	return os.NewFile(uintptr(h), path), nil
}

func isWinFileNotFound(err error) bool {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return errno == windows.ERROR_FILE_NOT_FOUND || errno == windows.ERROR_PATH_NOT_FOUND
	}
	return false
}

// returns the time the file was last modified
func getLastModifiedTime(path string) (time.Time, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return time.Time{}, err
	}

	return fi.ModTime(), nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()

	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

func writeZoneIdentifier(outputFilePath string, hostUrl string, chdCreationTime string, zipfileName string, zipfileSize int64, zipfileHash string, zipfileModificationTime time.Time, processFilename string, processFileSize int64, processFileHash string, processFileModificationTime time.Time, extraLines ...string) (ADSResult, error) {

	if DBGADS {
		fmt.Println("about to write Zone Identifier for ", outputFilePath)
	}

	abs, err := filepath.Abs(outputFilePath)
	if err != nil {
		if DBGADS {
			log.Error("got an error")
		}
		return ADSResult{}, err
	}

	// Make sure the base file exists.
	baseFile, err := openFileHandleForMetadata(abs)
	if err != nil {
		if DBGADS {
			fmt.Println(fmt.Errorf("unable to write NTFS: %w", err))
			return ADSResult{}, fmt.Errorf("unable to write NTFS: %w", err)
		}
	}

	defer baseFile.Close()

	supportsStreams, fsName, err := fileSupportsNamedStreams(baseFile)
	if err != nil {
		if DBGADS {
			log.Info("output filesystem isn't NTFS, so can't add output stream (not a problem, unless the output file IS on a NTFS volume. Then... well, it probably IS a problem.")
			return ADSResult{}, fmt.Errorf("check volume capabilities: %w", err)
		}
	}
	if !supportsStreams {
		// exFAT/FAT32/etc: clean "not supported", not a crash.
		if DBGADS {
			log.Info("output filesystem doesn't support streams. If it's NTFS, this is a problem. Otherwise, it's fine.")
			return ADSResult{Supported: false, Written: false}, nil
		}
	}

	adsPath := abs + ":Zone.Identifier"

	adsFile, err := createADSForWrite(adsPath)
	if err != nil {
		// If the volume says it supports streams but the open still fails,
		// return a real error so you can log/debug it.
		if DBGADS {
			log.Error("output filesystem says it supports ADS streams, but the attempt to open still failed.")
			return ADSResult{}, fmt.Errorf("open ADS for write on %s (%s): %w", abs, fsName, err)
		}
	}
	defer adsFile.Close()

	lines := []string{
		"[ZoneTransfer]",
		"ZoneId=3",
	}
	for _, s := range extraLines {
		if s != "" {
			lines = append(lines, s)
		}
	}
	if hostUrl != "" {
		lines = append(lines, "HostUrl="+hostUrl)
	} else {
		lines = append(lines, "HostUrl=Unknown")
	}

	lines = append(lines, "zipfile-Name="+zipfileName)
	lines = append(lines, fmt.Sprintf("zipfile-Size=%d", zipfileSize))
	lines = append(lines, "zipfile-SHA256="+zipfileHash)
	lines = append(lines, "zipfile-LastModified="+zipfileModificationTime.Format("2006-01-02 15:04:05 -0700 MST"))
	lines = append(lines, "processedFile="+processFilename)
	lines = append(lines, fmt.Sprintf("processedFile-Size=%d", processFileSize))
	lines = append(lines, "processedFile-SHA256="+processFileHash)
	lines = append(lines, "processedFile-LastModified="+processFileModificationTime.Format("2006-01-02 15:04:05 -0700 MST"))
	lines = append(lines, "chd-CreationDate="+chdCreationTime)

	// Windows text file style.
	payload := strings.Join(lines, "\r\n") + "\r\n"
	if DBGADS {
		fmt.Println("About to write ADS payload:\n", payload)
	}

	if _, err := adsFile.WriteString(payload); err != nil {
		return ADSResult{}, fmt.Errorf("write ADS: %w", err)
	}
	if err := adsFile.Sync(); err != nil {
		return ADSResult{}, fmt.Errorf("flush ADS: %w", err)
	}

	if DBGADS {
		log.Info("ADS updated")
	}
	return ADSResult{Supported: true, Written: true}, nil
}

func openFileHandleForMetadata(path string) (*os.File, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}

	h, err := windows.CreateFile(
		p,
		0, // metadata only is fine
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		return nil, err
	}

	return os.NewFile(uintptr(h), path), nil
}

func fileSupportsNamedStreams(f *os.File) (bool, string, error) {
	var volName [windows.MAX_PATH + 1]uint16
	var fsName [windows.MAX_PATH + 1]uint16
	var serial uint32
	var maxComponentLen uint32
	var fsFlags uint32

	err := windows.GetVolumeInformationByHandle(
		windows.Handle(f.Fd()),
		&volName[0],
		uint32(len(volName)),
		&serial,
		&maxComponentLen,
		&fsFlags,
		&fsName[0],
		uint32(len(fsName)),
	)
	if err != nil {
		return false, "", err
	}

	name := windows.UTF16ToString(fsName[:])
	return (fsFlags & windows.FILE_NAMED_STREAMS) != 0, name, nil
}

func createADSForWrite(adsPath string) (*os.File, error) {
	p, err := windows.UTF16PtrFromString(adsPath)
	if err != nil {
		return nil, err
	}

	h, err := windows.CreateFile(
		p,
		windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.CREATE_ALWAYS,
		windows.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		return nil, err
	}

	return os.NewFile(uintptr(h), adsPath), nil
}
