package main

import (
	"compress/zlib"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
)

type Type string

const (
	TypeObject Type = "object"
	TypeTree   Type = "tree"
	TypeCommit Type = "commit"
	TypeTag    Type = "tag"
)

type Object struct {
	Type    Type
	Size    int
	Content []byte
}

func parseType(data []byte) (_type Type, endIdx int) {
	endIdx = slices.Index(data, byte(' '))
	_type = Type(string(data[:endIdx]))
	return
}

func parseSize(data []byte, startIdx int) (size int, endIdx int, err error) {
	endIdxSliced := slices.Index(data[startIdx:], byte('\000'))
	endIdx = startIdx + endIdxSliced
	size, err = strconv.Atoi(string(data[startIdx:endIdx]))
	return
}

func parseObject(hash string) (*Object, error) {
	objectPath := filepath.Join(".git", "objects", hash[:2], hash[2:])

	f, err := os.Open(objectPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open %s: %s", objectPath, err.Error())
	}
	defer f.Close()

	r, err := zlib.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("failed to read zlib compressed file %s: %s", objectPath, err.Error())
	}
	defer r.Close()

	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("failed to read all from zlib compressed file %s: %s", objectPath, err.Error())
	}

	_type, typeEndIdx := parseType(data)
	size, sizeEndIdx, err := parseSize(data, typeEndIdx+1)
	if err != nil {
		return nil, fmt.Errorf("failed to parse size in file %s: %s", objectPath, err.Error())
	}
	content := data[sizeEndIdx+1:]
	return &Object{
		Type:    _type,
		Size:    size,
		Content: content,
	}, nil
}

// Usage: your_program.sh <command> <arg1> <arg2> ...
func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: mygit <command> [<args>...]\n")
		os.Exit(1)
	}

	switch command := os.Args[1]; command {
	case "init":
		for _, dir := range []string{".git", ".git/objects", ".git/refs"} {
			if err := os.MkdirAll(dir, 0755); err != nil {
				fmt.Fprintf(os.Stderr, "Error creating directory: %s\n", err)
			}
		}

		headFileContents := []byte("ref: refs/heads/main\n")
		if err := os.WriteFile(".git/HEAD", headFileContents, 0644); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing file: %s\n", err)
		}

		fmt.Println("Initialized git directory")
	case "cat-file":
		object, err := parseObject(os.Args[3])
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error on reading object %s\n", err.Error())
			os.Exit(1)
		}
		switch os.Args[2] {
		case "-t":
			fmt.Print(object.Type)
		case "-s":
			fmt.Print(object.Size)
		case "-p":
			fmt.Print(string(object.Content))
		default:
			fmt.Fprintf(os.Stderr, "Unknown command %s\n", os.Args)
			os.Exit(1)
		}

	default:
		fmt.Fprintf(os.Stderr, "Unknown command %s\n", command)
		os.Exit(1)
	}
}
