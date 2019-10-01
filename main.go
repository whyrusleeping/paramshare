package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"

	"github.com/urfave/cli"
	"gopkg.in/cheggaaa/pb.v1"
)

type FileInfo struct {
	Name string
	Size int64
	Hash string
}

func computeFileList(dir string) ([]FileInfo, error) {
	fmt.Println("computing file list")

	var out []FileInfo
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		fmt.Println("processing: ", dir)
		if err != nil {
			return fmt.Errorf("directory walk failed: %s", err)
		}

		if info.IsDir() {
			return nil
		}

		fi, err := os.Open(path)
		if err != nil {
			return err
		}
		defer fi.Close()

		h := sha256.New()

		nb, err := io.Copy(h, fi)
		if err != nil {
			return err
		}

		sum := h.Sum(nil)
		hashstr := hex.EncodeToString(sum[:])

		out = append(out, FileInfo{
			Name: filepath.Base(path),
			Size: nb,
			Hash: hashstr,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}

	return out, nil
}

func main() {
	app := cli.NewApp()
	app.Commands = []cli.Command{sendCmd, recvCmd}
	app.RunAndExitOnError()
}

var sendCmd = cli.Command{
	Name: "send",
	Action: func(cctx *cli.Context) error {
		dir := "/var/tmp/filecoin-proof-parameters/"
		target := cctx.Args().First()

		s, err := net.Dial("tcp", target)
		if err != nil {
			return err
		}
		defer s.Close()

		files, err := computeFileList(dir)
		if err != nil {
			return err
		}

		fmt.Println("files being offered:")
		for _, f := range files {
			fmt.Printf("%s\t%s\t%d\n", f.Name, f.Hash, f.Size)
		}

		filesenc, err := json.Marshal(files)
		if err != nil {
			return err
		}

		_, err = s.Write(filesenc)
		if err != nil {
			return err
		}

		dec := json.NewDecoder(s)

		for {
			var req FileInfo
			if err := dec.Decode(&req); err != nil {
				return err
			}

			if req.Name == "" {
				fmt.Println("done!")
				return nil
			}

			var found bool
			for _, f := range files {
				if f == req {
					found = true
					break
				}
			}

			if !found {
				return fmt.Errorf("requested file was not in our set of files")
			}

			fmt.Println("sending: ", req.Name)
			fi, err := os.Open(filepath.Join(dir, req.Name))
			if err != nil {
				return err
			}
			bar := pb.New64(req.Size)
			r := bar.NewProxyReader(fi)

			n, err := io.Copy(s, r)
			if err != nil {
				return err
			}

			if n != req.Size {
				return fmt.Errorf("failed to copy correct number of bytes")
			}
		}

		return nil
	},
}

var recvCmd = cli.Command{
	Name: "recv",
	Action: func(cctx *cli.Context) error {
		dir := "/var/tmp/filecoin-proof-parameters/"
		list := "0.0.0.0:15123"

		l, err := net.Listen("tcp", list)
		if err != nil {
			return err
		}

		files, err := computeFileList(dir)
		if err != nil {
			return err
		}

		fmt.Println("now waiting for connection from sender...")
		s, err := l.Accept()
		if err != nil {
			return err
		}

		fmt.Println("files we already have:")
		for _, f := range files {
			fmt.Printf("%s\t%s\t%d\n", f.Name, f.Hash, f.Size)
		}

		dec := json.NewDecoder(s)
		enc := json.NewEncoder(s)

		var available []FileInfo
		if err := dec.Decode(&available); err != nil {
			return err
		}

		for _, a := range available {
			var found bool
			for _, f := range files {
				if f.Name == a.Name {
					if f.Hash != a.Hash {
						fmt.Printf("sender has file %s with different hash\n", a.Name)
						break
					} else {
						found = true
						break
					}
				}
			}
			if found {
				continue
			}

			fmt.Printf("Requesting: %s\n", a.Name)
			if err := enc.Encode(&a); err != nil {
				return err
			}

			fi, err := os.Create(filepath.Join(dir, a.Name))
			if err != nil {
				return err
			}
			defer fi.Close()

			_, err = io.CopyN(fi, s, a.Size)
			if err != nil {
				return err
			}
		}
		if err := enc.Encode(FileInfo{}); err != nil {
			return err
		}

		return nil
	},
}
