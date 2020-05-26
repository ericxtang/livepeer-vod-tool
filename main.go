package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var playlists = map[string][]string{}

func main() {
	fname := flag.String("file", "", "File to transcode")
	apiKey := flag.String("apiKey", "", "Livepeer api key")
	presets := flag.String("presets", "", "Location of the json file that contains the transcode presets")

	flag.Parse()
	assertFfmpeg()

	if *fname == "" {
		fmt.Println("Need to specify a file to transcode")
		return
	}
	if *apiKey == "" {
		fmt.Println("Need to specify api-key")
		return
	}
	if *presets == "" {
		fmt.Println("Need to specify presets")
		return
	}

	streamID, err := createStream(*apiKey, *presets)
	if err != nil {
		fmt.Println("Failed to create stream.")
		return
	}

	broadcaster, err := getBroadcaster(*apiKey)
	if err != nil {
		fmt.Println("Failed to fetch broadcaster.")
		return
	}

	datadir := "./results"
	if err = os.MkdirAll(datadir, 0755); err != nil {
		fmt.Println("Error creating result directory")
		return
	}

	segment(*fname, datadir)

	if !transcode(broadcaster, streamID, *fname, datadir) {
		fmt.Println("Transcoding failed")
	}

	writePlaylists(*fname, datadir, playlists)
	fmt.Println("Finished.")
}

func assertFfmpeg() {
	cmd := exec.Command("which", "ffmpeg")
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	if err != nil {
		log.Fatal("Need to install ffmpeg first")
	}
}

func segment(fname, datadir string) {
	fmt.Println("Segmenting " + fname)
	extension := filepath.Ext(fname)
	nameNoExt := fname[0 : len(fname)-len(extension)]
	// cmd := exec.Command("ffmpeg", "-i", fname, "-acodec", "aac", "-f", "segment", "-vcodec", "copy", "-reset_timestamps", "1", "-map", "0", datadir+"/"+nameNoExt+"_%04d.mp4")
	cmd := exec.Command("ffmpeg", "-i", fname, "-acodec", "aac", "-f", "segment", "-vcodec", "copy", "-reset_timestamps", "1", "-map", "0", "-hls_list_size", "0", "-f", "hls", datadir+"/"+nameNoExt+".m3u8")

	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	if err != nil {
		fmt.Println("Error running command %v, %v, %v", cmd, err, out)
	}
}

func createStream(apiKey, presets string) (string, error) {
	bearer := "Bearer " + apiKey
	f, err := ioutil.ReadFile(presets)
	body := bytes.NewBuffer(f)

	req, err := http.NewRequest("POST", "https://livepeer.com/api/stream", body)
	if err != nil {
		return "", errors.New("")
	}

	req.Header.Add("Authorization", bearer)
	req.Header.Add("content-type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Println("Error creating stream: %s", err)
		return "", errors.New("")
	}
	defer resp.Body.Close()
	res, err := ioutil.ReadAll(resp.Body)
	if err != nil || string(res) == "" {
		fmt.Println("Error creating stream: %s", err)
		return "", errors.New("")
	}

	var stream map[string]interface{}
	err = json.Unmarshal([]byte(res), &stream)
	if err != nil {
		fmt.Println("Error parsing response: %v", err)
		return "", errors.New("")
	}
	// fmt.Println("Created Stream.\n%s\n", stream)

	return fmt.Sprintf("%v", stream["id"]), nil
}

func getBroadcaster(apiKey string) (string, error) {
	bearer := "Bearer " + apiKey
	req, err := http.NewRequest("GET", "https://livepeer.com/api/broadcaster", nil)
	if err != nil {
		return "", errors.New("")
	}

	req.Header.Add("Authorization", bearer)
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Println("Error: %s", err)
		return "", errors.New("")
	}
	defer resp.Body.Close()
	res, err := ioutil.ReadAll(resp.Body)
	if err != nil || string(res) == "" {
		fmt.Println("Error getting broadcasters: %s", err)
		return "", errors.New("")
	}

	var broadcasters []map[string]string
	err = json.Unmarshal([]byte(res), &broadcasters)
	if err != nil {
		fmt.Println("Error parsing response: %v", err)
		return "", errors.New("")
	}
	// fmt.Println("Broadcasters:\n%s\n", broadcasters)

	return fmt.Sprintf("%v", broadcasters[0]["address"]), nil
}

func transcode(broadcaster, streamID, fname, datadir string) bool {
	files, _ := ioutil.ReadDir(datadir)
	fmt.Printf("Created %v segments\n", len(files))
	for i := 0; i < len(files); i++ {
		extension := filepath.Ext(fname)
		nameNoExt := fname[0 : len(fname)-len(extension)]
		segName := fmt.Sprintf("%s%d.ts", nameNoExt, i)
		fmt.Printf("Transcoding %v\n", segName)

		// extension := filepath.Ext(fname)
		// nameNoExt := fname[0 : len(fname)-len(extension)]
		segDuration := readDuration(datadir+"/"+nameNoExt+".m3u8", segName)
		if err := transcodeSeg(datadir, segName, fmt.Sprintf("%d", i), broadcaster, streamID, segDuration); err != nil {
			fmt.Printf("Failed to transcode %v: %v", segName, err)
			// return false
		}
	}

	return true
}

func transcodeSeg(datadir, fname, i, broadcaster, streamID, duration string) error {
	f, err := ioutil.ReadFile(datadir + "/" + fname)
	body := bytes.NewBuffer(f)

	req, err := http.NewRequest("POST", fmt.Sprintf("%v/live/%v/%v.ts", broadcaster, streamID, i), body)
	if err != nil {
		return err
	}

	req.Header.Add("Accept", "multipart/mixed")
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Println("Error: %s", err)
		return err
	}
	defer resp.Body.Close()
	mediaType, params, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil {
		return err
	}

	if strings.HasPrefix(mediaType, "multipart/") {
		mr := multipart.NewReader(resp.Body, params["boundary"])
		for {
			p, err := mr.NextPart()
			if err == io.EOF {
				return nil
			}
			if err != nil {
				return err
			}

			h := p.Header["Content-Disposition"]
			_, params, err := mime.ParseMediaType(h[0])
			tfname := params["filename"]
			content, err := ioutil.ReadAll(p)
			ioutil.WriteFile(datadir+"/"+string(tfname[0:5])+fname, content, 0666)
			insertToPlaylist(string(tfname[0:5]), i, string(tfname[0:5])+fname, duration)
		}
	}
	return nil
}

func insertToPlaylist(plName, i, filename, duration string) {
	if playlists[plName] == nil {
		playlists[plName] = []string{}
	}
	playlists[plName] = append(playlists[plName], duration)
	playlists[plName] = append(playlists[plName], filename)
}

func readDuration(plName, fname string) string {
	file, err := os.Open(plName)
	defer file.Close()

	if err != nil {
		return ""
	}

	// Start reading from the file using a scanner.
	scanner := bufio.NewScanner(file)

	duration := ""
	for scanner.Scan() {
		line := scanner.Text()

		if len(line) > 8 && string(line[0:8]) == "#EXTINF:" {
			duration = line
		}
		if line == fname {
			return duration
		}
	}

	if scanner.Err() != nil {
		fmt.Printf(" > Failed!: %v\n", scanner.Err())
	}

	return ""
}

//This is definitely pretty lazy.  The write way to do it would be load the presets and set the playlist params based on that.
func writePlaylists(fname, datadir string, playlists map[string][]string) {
	pl, _ := os.Create(datadir + "/playlist.m3u8")
	defer pl.Close()
	plw := bufio.NewWriter(pl)
	plw.WriteString("#EXTM3U\n")

	for plName, entries := range playlists {
		f, _ := os.Create(datadir + "/" + plName + ".m3u8")
		defer f.Close()
		w := bufio.NewWriter(f)
		w.WriteString("#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:3\n#EXT-X-MEDIA-SEQUENCE:0\n")
		for _, l := range entries {
			w.WriteString(l + "\n")
		}
		w.WriteString("#EXT-X-ENDLIST\n")
		w.Flush()

		if plName == "1080p" {
			plw.WriteString("#EXT-X-STREAM-INF:PROGRAM-ID=1,BANDWIDTH=2000064,RESOLUTION=1920x1080,FRAME-RATE=30,CODECS=\"avc1.64001f\"\n")
			plw.WriteString("1080p.m3u8\n")
		} else if plName == "720p_" {
			plw.WriteString("#EXT-X-STREAM-INF:PROGRAM-ID=1,BANDWIDTH=1601536,RESOLUTION=1280x720,FRAME-RATE=30,CODECS=\"avc1.4d401f\"\n")
			plw.WriteString("720p_.m3u8\n")
		} else if plName == "360p_" {
			plw.WriteString("#EXT-X-STREAM-INF:PROGRAM-ID=1,BANDWIDTH=501736,RESOLUTION=640x360,FRAME-RATE=30,CODECS=\"avc1.4d401e\"\n")
			plw.WriteString("360p_.m3u8\n")
		}

	}
	plw.Flush()
}
