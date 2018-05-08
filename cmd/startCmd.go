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
	"net/http/httputil"
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
		conn, resp, err := d.Dial(u.String(), reqH)
		if err != nil {
			log.Printf("Error dialing websocket: %v\n", err)
			return
		}
		defer check.Err(conn.Close)

		if appEnv == "dev" {
			respDump, err := httputil.DumpResponse(resp, true)
			if err != nil {
				log.Println(err)
			}
			log.Println(string(respDump))
		}

		response := make(chan []byte)
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
						return
					}
					j := &job.Job{}
					err = gob.NewDecoder(zr).Decode(j)
					if err != nil {
						log.Printf("Error decoding message: %v\n", err)
						return
					}
					log.Printf("Received job: %+v\n", j)
					response <- []byte("I hear you")
				case websocket.TextMessage:
					log.Printf("TextMessage: ")
					_, err = io.Copy(os.Stdout, r)
					if err != nil {
						log.Printf("Error copying websocket.TextMessage to os.Stdout: %v\n", err)
					}
				default:
					log.Printf("Non-text or binary websocket message received. Closing.\n")
					return
				}
			}
		}()

		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-done:
				return
			// case t := <-ticker.C:
			// 	err := conn.WriteMessage(websocket.TextMessage, []byte(t.String()))
			// 	if err != nil {
			// 		log.Printf("Error writing message: %v\n", err)
			// 		return
			// 	}
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
