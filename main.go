package main

// #cgo LDFLAGS: -lalpm
// #include <alpm.h>
import "C"

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

var dbname = "aur"
var handle = C.alpm_initialize(C.CString("/"), C.CString("/var/lib/pacman/"), nil)
var db = C.alpm_register_syncdb(handle, C.CString(dbname), 0)
var pkgdest = filepath.Join(os.Getenv("HOME"), ".cache", dbname)
var dbpath = filepath.Join(pkgdest, dbname+".db.tar.gz")
var re = regexp.MustCompile(`.*/(.*)-(.*?-.*?)-.*?\.pkg\.tar\.zst`)
var force = false
var devel = false
var noedit = false

type Package struct {
	Description string  `json:"Description"`
	Maintainer  string  `json:"Maintainer"`
	Name        string  `json:"Name"`
	NumVotes    int     `json:"NumVotes"`
	OutOfDate   int64   `json:"OutOfDate"`
	PackageBase string  `json:"PackageBase"`
	Popularity  float64 `json:"Popularity"`
	Version     string  `json:"Version"`
	OldVersion  string
}

type Result struct {
	ResultCount int       `json:"resultcount"`
	Results     []Package `json:"results"`
}

func get(u *url.URL) []Package {
	res, err := http.Get(u.String())
	if err != nil {
		log.Fatal(err)
	}
	var result Result
	if err := json.NewDecoder(res.Body).Decode(&result); err != nil {
		log.Fatal(err)
	}
	return result.Results
}

func fetch(names []string) []Package {
	fmt.Println("\033[1;34m::\033[39m Fetching packages...\033[0m")
	u, err := url.Parse("https://aur.archlinux.org/rpc/v5/info")
	if err != nil {
		log.Fatal(err)
	}
	u.RawQuery = url.Values{"arg[]": names}.Encode()
	return get(u)
}

func remove(names []string) {
	arg := []string{dbpath}
	arg = append(arg, names...)
	cmd := exec.Command("repo-remove", arg...)
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout
	if err := cmd.Run(); err != nil {
		log.Fatal(err)
	}
}

func search(str string) {
	u, err := url.Parse("https://aur.archlinux.org/rpc/v5/search")
	if err != nil {
		log.Fatal(err)
	}
	pkgs := get(u.JoinPath(str))
	sort.Slice(pkgs, func(i, j int) bool {
		if pkgs[i].Popularity == pkgs[j].Popularity {
			return pkgs[i].NumVotes < pkgs[j].NumVotes
		}
		return pkgs[i].Popularity < pkgs[j].Popularity
	})
	for _, p := range pkgs {
		fmt.Printf("\033[1;35maur/\033[39m%s \033[32m%s\033[39m \033[36m[%d %f]\033[0m", p.Name, p.Version, p.NumVotes, p.Popularity)
		if p.OutOfDate > 0 {
			date := time.Unix(p.OutOfDate, 0).Format(time.DateOnly)
			fmt.Printf(" \033[31m%s\033[39m", date)
		}
		fmt.Println("\n   ", p.Description)
	}
}

func makepkg(base string, arg ...string) *exec.Cmd {
	cmd := exec.Command("makepkg", arg...)
	cmd.Env = append(cmd.Environ(), "PKGDEST="+pkgdest, "BUILDDIR="+os.TempDir())
	cmd.Dir = filepath.Join(pkgdest, base)
	return cmd
}

func VCSVersion(base string) map[string]string {
	cmd := makepkg(base, "--nobuild", "--nodeps", "--noprepare")
	if err := cmd.Run(); err != nil {
		log.Fatal(err)
	}
	cmd = makepkg(base, "--packagelist")
	output, err := cmd.Output()
	if err != nil {
		log.Fatal(err)
	}
	version := make(map[string]string)
	for _, str := range strings.Split(string(output), " ") {
		match := re.FindStringSubmatch(str)
		version[match[1]] = match[2]
	}
	return version
}

func prepare(names []string) []Package {
	pkgs := fetch(names)
	if len(names) != len(pkgs) {
		nfs := make(map[string]struct{})
		for _, name := range names {
			nfs[name] = struct{}{}
		}
		for _, p := range pkgs {
			delete(nfs, p.Name)
		}
		if len(nfs) > 0 {
			str := []string{}
			for n := range nfs {
				str = append(str, n)
			}
			log.Fatal("target not found: ", strings.Join(str, " "))
		}
	}
	for _, p := range pkgs {
		if p.OutOfDate != 0 {
			date := time.Unix(p.OutOfDate, 0).Format(time.DateOnly)
			fmt.Printf("\033[1;33m==> WARNING:\033[39m %s is flagged out of date (%s)\033[0m\n", p.Name, date)
		}
		if p.Maintainer == "" {
			fmt.Printf("\033[1;33m==> WARNING:\033[39m %s is orphan\033[0m\n", p.Name)
		}
	}
	outdated := []Package{}
	for _, p := range pkgs {
		pkg := C.alpm_db_get_pkg(db, C.CString(p.Name))
		if pkg != nil {
			if devel && strings.HasSuffix(p.Name, "-git") {
				p.Version = VCSVersion(p.PackageBase)[p.Name]
			}
			p.OldVersion = C.GoString(C.alpm_pkg_get_version(pkg))
		}
		if force || C.alpm_pkg_vercmp(C.CString(p.Version), C.CString(p.OldVersion)) > 0 {
			outdated = append(outdated, p)
		}
	}
	if len(outdated) > 0 {
		nlen := 0
		vlen := 0
		clen := len(fmt.Sprintf("%d", len(outdated)))
		for _, p := range outdated {
			nlen = max(nlen, len(p.Name))
			vlen = max(vlen, len(p.OldVersion))
		}
		nlen = max(nlen, 10+clen)
		if vlen != 0 {
			vlen = max(vlen, 11)
		}
		fmt.Println("\033[1m")
		fmt.Printf("%-*s  ", nlen, fmt.Sprintf("Package (%d)", len(outdated)))
		if vlen != 0 {
			fmt.Printf("Old Version  %*s", vlen, "New Version")
		} else {
			fmt.Print("New Version")
		}
		fmt.Println("\033[0m\n")
		sort.Slice(outdated, func(i, j int) bool { return outdated[i].Name < outdated[j].Name })
		for _, p := range outdated {
			if vlen != 0 {
				fmt.Printf("%-*s  %-*s  %s\n", nlen, p.Name, vlen, p.OldVersion, p.Version)
			} else {
				fmt.Printf("%-*s  %s\n", nlen, p.Name, p.Version)
			}
		}
		fmt.Println()
	} else {
		fmt.Println("There is nothing to do")
		os.Exit(0)
	}
	return outdated
}

func build(base string) {
	cmd := makepkg(base, "--force", "--syncdeps")
	cmd.Stdin = os.Stdin
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout
	if err := cmd.Run(); err != nil {
		log.Fatal(err)
	}
	cmd = makepkg(base, "--packagelist")
	output, err := cmd.Output()
	if err != nil {
		log.Fatal(err)
	}
	pkgs := strings.Split(strings.TrimSpace(string(output)), "\n")
	arg := []string{"--remove", dbpath}
	arg = append(arg, pkgs...)
	cmd = exec.Command("repo-add", arg...)
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout
	if err := cmd.Run(); err != nil {
		log.Fatal(err)
	}
}

func prompt(str string) bool {
	fmt.Printf("\033[1;34m::\033[39m %s [Y/n]\033[0m ", str)
	var ans string
	fmt.Scanln(&ans)
	switch ans {
	case "y", "Y", "":
		return true
	}
	return false
}

func editPKGBUILD(src string) {
	if !noedit && prompt("Edit PKGBUILD?") {
		cmd := exec.Command("vim", filepath.Join(src, "PKGBUILD"))
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		if err := cmd.Run(); err != nil {
			log.Fatal(err)
		}
	}
}

func sync(names []string) {
	pkgs := prepare(names)
	if !prompt("Proceed with synchronising?") {
		os.Exit(1)
	}
	bases := make(map[string]struct{})
	for _, p := range pkgs {
		bases[p.PackageBase] = struct{}{}
	}
	for base := range bases {
		fmt.Printf("\033[1;34m::\033[39m Syncing: %s\n", base)
		src := filepath.Join(pkgdest, base)
		if _, err := os.Stat(src); err != nil {
			url := "https://aur.archlinux.org/" + base + ".git"
			cmd := exec.Command("git", "clone", url, src)
			if err := cmd.Run(); err != nil {
				log.Fatal(err)
			}
		}
		editPKGBUILD(src)
		build(base)
	}
}

func git(src string, arg ...string) *exec.Cmd {
	args := []string{"-C", src}
	args = append(args, arg...)
	cmd := exec.Command("git", args...)
	return cmd
}

func update() {
	names := []string{}
	cache := C.alpm_db_get_pkgcache(db)
	for cache != nil {
		pkg := (*C.alpm_pkg_t)(cache.data)
		name := C.GoString(C.alpm_pkg_get_name(pkg))
		names = append(names, name)
		cache = cache.next
	}
	pkgs := prepare(names)
	if !prompt("Proceed with updating?") {
		os.Exit(1)
	}
	bases := make(map[string]struct{})
	for _, p := range pkgs {
		bases[p.PackageBase] = struct{}{}
	}
	for base := range bases {
		fmt.Printf("\033[1;34m::\033[39m Updating: %s\n", base)
		src := filepath.Join(pkgdest, base)
		cmd := git(src, "fetch", "--quiet")
		if err := cmd.Run(); err != nil {
			log.Fatal(err)
		}
		cmd = git(src, "diff", "HEAD", "FETCH_HEAD", "--quiet")
		if err := cmd.Run(); err != nil {
			if exit, ok := err.(*exec.ExitError); ok {
				if exit.ExitCode() == 1 && prompt("Show diff?") {
					cmd = git(src, "diff", "HEAD", "FETCH_HEAD")
					cmd.Stdout = os.Stdout
					if err := cmd.Run(); err != nil {
						log.Fatal(err)
					}
				}
			}
		}
		cmd = git(src, "merge", "--quiet")
		if err := cmd.Run(); err != nil {
			log.Fatal(err)
		}
		editPKGBUILD(src)
		build(base)
	}
}

func clean() {
	filenames := make(map[string]struct{})
	cache := C.alpm_db_get_pkgcache(db)
	for cache != nil {
		pkg := (*C.alpm_pkg_t)(cache.data)
		base := C.GoString(C.alpm_pkg_get_base(pkg))
		filename := C.GoString(C.alpm_pkg_get_filename(pkg))
		filenames[base] = struct{}{}
		filenames[filename] = struct{}{}
		cache = cache.next
	}
	files, err := os.ReadDir(pkgdest)
	if err != nil {
		log.Fatal(err)
	}
	garbage := []string{}
	fmt.Println("removing old packages from cache...")
	for _, file := range files {
		name := file.Name()
		if _, ok := filenames[name]; ok || strings.HasPrefix(name, dbname+".") {
			if file.IsDir() {
				src := filepath.Join(pkgdest, name)
				cmd := git(src, "clean", "-dfx")
				if err := cmd.Run(); err != nil {
					log.Fatal(err)
				}
			}
		} else {
			garbage = append(garbage, name)
		}
	}
	fmt.Println("removing unsynced packages...")
	for _, name := range garbage {
		if err := os.RemoveAll(filepath.Join(pkgdest, name)); err != nil {
			log.Fatal(err)
		}
	}
}

func usage() {
	fmt.Println(`usage: aur <operation>
operations:
    clean
    remove [package(s)]
    search [string]
    sync   [options] [package(s)]
    update [options]
options:
    --devel   check development packages during update
    --force   always sync packages
    --noedit  don't edit PKGBUILD`)
	os.Exit(0)
}

func parser() (string, []string) {
	args := []string{}
	for _, a := range os.Args[1:] {
		if strings.HasPrefix(a, "-") {
			switch a[2:] {
			case "help":
				usage()
			case "devel":
				devel = true
			case "force":
				force = true
			case "noedit":
				noedit = true
			default:
				log.Fatal("invalid option: ", a)
			}
		} else {
			args = append(args, a)
		}
	}
	if len(args) < 1 {
		log.Fatal("no operation specified")
	}
	return args[0], args[1:]
}

func main() {
	op, args := parser()
	switch op {
	case "clean":
		clean()
	case "remove":
		remove(args)
	case "search":
		search(strings.Join(args, ""))
	case "sync":
		sync(args)
	case "update":
		update()
	default:
		log.Fatal("unknown operation: ", op)
	}
}
