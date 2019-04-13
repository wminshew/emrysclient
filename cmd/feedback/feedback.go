package feedback

import (
	"context"
	"fmt"
	"github.com/cenkalti/backoff"
	"github.com/dgrijalva/jwt-go"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/wminshew/emrys/pkg/check"
	"github.com/wminshew/emrysclient/pkg/token"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"
)

const (
	maxRetries = 10
)

func init() {
	Cmd.PersistentFlags().StringP("message", "m", "", "Feedback message")
	Cmd.Flags().SortFlags = false
	if err := func() error {
		if err := viper.BindPFlag("message", Cmd.PersistentFlags().Lookup("message")); err != nil {
			return err
		}
		return nil
	}(); err != nil {
		log.Printf("Feedback: error binding pflag: %v", err)
		panic(err)
	}
}

// Cmd exports feedback subcommand to root
var Cmd = &cobra.Command{
	Use:   "feedback",
	Short: "Send feedback to emrys",
	Long:  "Send feedback to emrys",
	Run: func(cmd *cobra.Command, args []string) {
		if os.Geteuid() != 0 {
			log.Printf("Insufficient privileges. Are you root?\n")
			return
		}

		message := viper.GetString("message")
		if message == "" {
			log.Printf("Feedback: no message included (use --message or -m \"Here's some feedback for you!\")")
			return
		}

		authToken, err := token.Get()
		if err != nil {
			log.Printf("Feedback: error retrieving authToken: %v", err)
			return
		}
		claims := &jwt.StandardClaims{}
		if _, _, err := new(jwt.Parser).ParseUnverified(authToken, claims); err != nil {
			log.Printf("Feedback: error parsing authToken: %v", err)
			return
		}
		if err := claims.Valid(); err != nil {
			log.Printf("Feedback: invalid authToken: %v", err)
			log.Printf("Feedback: please login again.\n")
			return
		}
		exp := claims.ExpiresAt
		refreshAt := time.Unix(exp, 0).Add(token.RefreshBuffer)
		if refreshAt.Before(time.Now()) {
			log.Printf("Feedback: token too close to expiration, please login again.")
			return
		}

		ctx := context.Background()
		client := &http.Client{}
		s := "https"
		h := "api.emrys.io"
		p := path.Join("user", "feedback")
		u := url.URL{
			Scheme: s,
			Host:   h,
			Path:   p,
		}
		operation := func() error {
			req, err := http.NewRequest(http.MethodPost, u.String(), strings.NewReader(message))
			if err != nil {
				return err
			}
			req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", authToken))
			req = req.WithContext(ctx)

			resp, err := client.Do(req)
			if err != nil {
				return err
			}
			defer check.Err(resp.Body.Close)

			if resp.StatusCode == http.StatusBadGateway {
				return fmt.Errorf("server: temporary error")
			} else if resp.StatusCode >= 300 {
				b, _ := ioutil.ReadAll(resp.Body)
				return backoff.Permanent(fmt.Errorf("server: %v", string(b)))
			}

			return nil
		}
		if err := backoff.RetryNotify(operation,
			backoff.WithContext(backoff.WithMaxRetries(backoff.NewExponentialBackOff(), maxRetries), ctx),
			func(err error, t time.Duration) {
				log.Printf("Feedback: error sending requirements: %v", err)
				log.Printf("Retrying in %s seconds\n", t.Round(time.Second).String())
			}); err != nil {
			log.Printf("Feedback: error sending requirements: %v", err)
			return
		}

		log.Printf("Feedback: received! Thank you for contributing\n")
	},
}
