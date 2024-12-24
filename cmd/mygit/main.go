package main

import (
	"bytes"
	"compress/zlib"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"syscall"
)

const mode = 0755

type Type string

const (
	TypeBlob   Type = "blob"
	TypeTree   Type = "tree"
	TypeCommit Type = "commit"
	TypeTag    Type = "tag"
)

type Object struct {
	Type    Type
	Size    int
	Content []byte
}

func getObjectDir(hash string) string {
	return filepath.Join(".git", "objects", hash[:2])
}

func getObjectPath(hash string) string {
	return filepath.Join(getObjectDir(hash), hash[2:])
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
	objectPath := getObjectPath(hash)

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

func createObjectDir(hash string) error {
	objectDir := getObjectDir(hash)
	if _, err := os.Stat(objectDir); os.IsNotExist(err) {
		return os.MkdirAll(objectDir, mode)
	}
	return nil
}

func hashObject(filename string) (string, error) {
	srcF, err := os.Open(filename)
	if err != nil {
		return "", fmt.Errorf("failed to open %s: %s", filename, err.Error())
	}
	defer srcF.Close()

	content, err := io.ReadAll(srcF)
	if err != nil {
		return "", fmt.Errorf("failed to read all from file %s: %s", filename, err.Error())
	}

	_type := []byte(TypeBlob)

	size := len(content)
	sizeBytes := []byte(strconv.Itoa(size))

	data := make([]byte, 0, len(_type)+1+len(sizeBytes)+1+len(content))
	data = append(data, _type...)
	data = append(data, byte(' '))
	data = append(data, sizeBytes...)
	data = append(data, byte('\000'))
	data = append(data, content...)

	hasher := sha1.New()
	hasher.Write(data)
	hash := hex.EncodeToString(hasher.Sum(nil))

	var b bytes.Buffer
	w := zlib.NewWriter(&b)
	w.Write(data)
	w.Close()

	err = createObjectDir(hash)
	if err != nil {
		return "", fmt.Errorf("failed create object dir for hash %s: %s", hash, err.Error())
	}

	err = os.WriteFile(getObjectPath(hash), b.Bytes(), mode)
	if err != nil {
		return "", fmt.Errorf("failed write to object file for hash %s: %s", hash, err.Error())
	}

	return hash, nil
}

type TreeObjectLine struct {
	Mode int
	Name string
	Hash []byte
}

func parseModeName(line []byte) (mode int, name string, err error) {
	lineParts := bytes.Split(line, []byte(" "))
	if len(lineParts) != 2 {
		return 0, "", fmt.Errorf("tree line is invalid")
	}

	mode, err = strconv.Atoi(string(lineParts[0]))
	if len(lineParts) != 2 {
		return 0, "", fmt.Errorf("error parsing mode")
	}
	name = string(lineParts[1])
	return
}

func decodeTreeObjectContent(content []byte) (string, error) {
	// <mode> <name>\0<20_byte_sha>
	contentPart := content
	treeObjectLines := make([]TreeObjectLine, 0)
	for {
		nullByteIdx := slices.Index(contentPart, byte('\000'))
		mode, name, err := parseModeName(contentPart[:nullByteIdx])
		if err != nil {
			return "", err
		}
		hash := contentPart[nullByteIdx+1 : nullByteIdx+21]
		treeObjectLines = append(treeObjectLines, TreeObjectLine{Mode: mode, Name: name, Hash: hash})

		if nullByteIdx+22 > len(contentPart) {
			break
		}
		contentPart = contentPart[nullByteIdx+22:]
	}

	output := ""
	for _, v := range treeObjectLines {
		output += v.Name + "\n"
	}
	return output, nil
}

// Usage: your_program.sh <command> <arg1> <arg2> ...
func main() {
	syscall.Umask(0)
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
		if err := os.WriteFile(".git/HEAD", headFileContents, mode); err != nil {
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
	case "hash-object":
		hash, err := hashObject(os.Args[3])
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error on hashing object %s\n", err.Error())
			os.Exit(1)
		}
		fmt.Print(string(hash))
	case "ls-tree":
		object, err := parseObject(os.Args[3])
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error on reading object %s\n", err.Error())
			os.Exit(1)
		}
		out, err := decodeTreeObjectContent(object.Content)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error on parsing tree object %s\n", err.Error())
			os.Exit(1)
		}
		fmt.Print(out)
	default:
		fmt.Fprintf(os.Stderr, "Unknown command %s\n", command)
		os.Exit(1)
	}
}
