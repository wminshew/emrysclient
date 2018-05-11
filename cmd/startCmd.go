package cmd

import (
	"compress/zlib"
	"encoding/gob"
	"fmt"
	"github.com/gorilla/websocket"
	"github.com/spf13/cobra"
	"github.com/wminshew/check"
	"github.com/wminshew/emrys/pkg/job"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"time"
)

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Begin mining on emrys",
	Long: `Start executing deep learning jobs
for money. When no jobs are available, or if the
asking rates are below your minimum, emrysminer
will default to the mining command provided in
./mining-script.sh.`,
	Run: func(cmd *cobra.Command, args []string) {
		token := getToken()

		h := resolveHost()
		u := url.URL{
			Scheme: "wss",
			Host:   h,
			Path:   "/miner/connect",
		}
		log.Printf("Connecting to %s...\n", u.String())
		o := url.URL{
			Scheme: "https",
			Host:   h,
		}
		d := websocket.DefaultDialer
		d.TLSClientConfig = resolveTLSConfig()
		reqH := http.Header{}
		reqH.Set("Authorization", fmt.Sprintf("Bearer %v", token))
		reqH.Set("Origin", o.String())
		conn, _, err := d.Dial(u.String(), reqH)
		if err != nil {
			log.Printf("Error dialing websocket: %v\n", err)
			return
		}
		defer check.Err(conn.Close)

		response := make(chan []byte)
		bid := make(chan *job.Bid)
		done := make(chan struct{})
		interrupt := make(chan os.Signal, 1)

		go func() {
			defer close(done)
			for {
				msgType, r, err := conn.NextReader()
				if err != nil {
					log.Printf("Error reading message: %v\n", err)
					return
				}
				switch msgType {
				case websocket.BinaryMessage:
					zr, err := zlib.NewReader(r)
					if err != nil {
						log.Printf("Error decompressing message: %v\n", err)
						break
					}
					j := &job.Job{}
					err = gob.NewDecoder(zr).Decode(j)
					if err != nil {
						log.Printf("Error decoding message: %v\n", err)
						break
					}
					err = zr.Close()
					if err != nil {
						log.Printf("Error closing zlib reader: %v\n", err)
						break
					}
					log.Printf("Received job: %+v\n", j)
					b := &job.Bid{
						JobID:   j.ID,
						MinRate: 0.2,
					}
					log.Printf("Sending bid: %+v\n", b)
					bid <- b
				case websocket.TextMessage:
					log.Print("TextMessage: ")
					_, err = io.Copy(os.Stdout, r)
					if err != nil {
						log.Printf("Error copying websocket.TextMessage to os.Stdout: %v\n", err)
						break
					}
				default:
					log.Printf("Non-text or -binary websocket message received. Closing.\n")
					break
				}
			}
		}()

		for {
			select {
			case <-done:
				return
			case b := <-bid:
				w, err := conn.NextWriter(websocket.BinaryMessage)
				if err != nil {
					log.Printf("Error generating next bid writer: %v\n", err)
					return
				}
				zw := zlib.NewWriter(w)
				err = gob.NewEncoder(zw).Encode(b)
				if err != nil {
					log.Printf("Error encoding bid: %v\n", err)
					break
				}
				err = zw.Close()
				if err != nil {
					log.Printf("Error closing zlib bid writer: %v\n", err)
					break
				}
				err = w.Close()
				if err != nil {
					log.Printf("Error closing conn bid writer: %v\n", err)
					return
				}
			case r := <-response:
				err := conn.WriteMessage(websocket.TextMessage, r)
				if err != nil {
					log.Printf("Error writing message: %v\n", err)
					return
				}
			case <-interrupt:
				log.Println("interrupt")

				err := conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
				if err != nil {
					log.Printf("Error writing close: %v\n", err)
					return
				}
				select {
				case <-done:
				case <-time.After(time.Second):
				}
				return
			}
		}
	},
}
