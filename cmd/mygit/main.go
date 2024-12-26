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
	"sort"
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

func saveObjectFile(content []byte, hash []byte) error {
	var b bytes.Buffer
	w := zlib.NewWriter(&b)
	w.Write(content)
	w.Close()

	hashStr := hex.EncodeToString(hash)
	err := createObjectDir(hashStr)
	if err != nil {
		return fmt.Errorf("failed create object dir for hash %s: %s", hashStr, err.Error())
	}

	err = os.WriteFile(getObjectPath(hashStr), b.Bytes(), mode)
	if err != nil {
		return fmt.Errorf("failed write to object file for hash %s: %s", hashStr, err.Error())
	}
	return nil
}

func calculateObjectBytesHash(data []byte) []byte {
	hasher := sha1.New()
	hasher.Write(data)
	return hasher.Sum(nil)
}

func writeBlobObject(filename string) ([]byte, error) {
	srcF, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to open %s: %s", filename, err.Error())
	}
	defer srcF.Close()

	content, err := io.ReadAll(srcF)
	if err != nil {
		return nil, fmt.Errorf("failed to read all from file %s: %s", filename, err.Error())
	}

	lineStr := fmt.Sprintf("%s %d\u0000", TypeBlob, len(content))
	lineBytes := []byte(lineStr)
	lineBytes = append(lineBytes, content...)

	hashBytes := calculateObjectBytesHash(lineBytes)
	err = saveObjectFile(lineBytes, hashBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to save file %s: %s", filename, err.Error())
	}
	return hashBytes, nil
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
	treeObjectLines := make([]TreeObjectLine, 0, 10)
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

func writeTreeObject(dirPath string) ([]byte, error) {
	files, err := os.ReadDir(dirPath)
	if err != nil {
		return nil, err
	}

	type entry struct {
		fileName  string
		lineBytes []byte
	}

	entries := make([]entry, 0, len(files)-1)
	totalSize := 0
	for _, file := range files {

		if file.Name() == ".git" {
			continue
		}

		fileInfo, err := file.Info()
		if err != nil {
			return nil, err
		}

		if fileInfo.IsDir() {
			hashBytes, err := writeTreeObject(filepath.Join(dirPath, fileInfo.Name()))
			if err != nil {
				return nil, err
			}
			lineStr := fmt.Sprintf("40000 %s\u0000", fileInfo.Name())
			lineBytes := append([]byte(lineStr), hashBytes...)
			entries = append(entries, entry{fileInfo.Name(), lineBytes})
			totalSize += len(lineBytes)
		} else {
			hashBytes, err := writeBlobObject(filepath.Join(dirPath, fileInfo.Name()))
			if err != nil {
				return nil, err
			}
			lineStr := fmt.Sprintf("%o %s\u0000", os.FileMode(0o100000)|fileInfo.Mode().Perm(), fileInfo.Name())
			lineBytes := append([]byte(lineStr), hashBytes...)
			entries = append(entries, entry{fileInfo.Name(), lineBytes})
			totalSize += len(lineBytes)
		}
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].fileName < entries[j].fileName })
	lineStr := fmt.Sprintf("%s %d\u0000", TypeTree, totalSize)
	lineBytes := []byte(lineStr)
	for _, entry := range entries {
		lineBytes = append(lineBytes, entry.lineBytes...)
	}
	hashBytes := calculateObjectBytesHash(lineBytes)

	err = saveObjectFile(lineBytes, hashBytes)
	if err != nil {
		return nil, err
	}

	return hashBytes, nil
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
		hash, err := writeBlobObject(os.Args[3])
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error on hashing object %s\n", err.Error())
			os.Exit(1)
		}
		fmt.Print(string(hex.EncodeToString(hash)))
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
	case "write-tree":
		hash, err := writeTreeObject(".")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error on writing tree %s\n", err.Error())
			os.Exit(1)
		}
		fmt.Print(string(hex.EncodeToString(hash)))
	default:
		fmt.Fprintf(os.Stderr, "Unknown command %s\n", command)
		os.Exit(1)
	}
}
