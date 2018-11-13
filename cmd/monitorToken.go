package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/cenkalti/backoff"
	"github.com/dgrijalva/jwt-go"
	"github.com/wminshew/emrys/pkg/check"
	"github.com/wminshew/emrys/pkg/creds"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"path"
	"time"
)

const (
	refreshDurationBuffer = -5 * time.Minute
	maxRetries            = 10
)

func monitorToken(ctx context.Context, client *http.Client, u url.URL, authToken *string, initialRefreshAt time.Time) {
	u.Path = path.Join("auth", "token")
	refreshAt := initialRefreshAt
	for {
		select {
		case <-ctx.Done():
		case <-time.After(time.Until(refreshAt)):
			loginResp := creds.LoginResp{}
			operation := func() error {
				req, err := http.NewRequest("POST", u.String(), nil)
				if err != nil {
					return err
				}
				req = req.WithContext(ctx)
				req.Header.Set("Authorization", fmt.Sprintf("Bearer %v", *authToken))
				q := req.URL.Query()
				q.Set("grant_type", "token")
				req.URL.RawQuery = q.Encode()

				resp, err := client.Do(req)
				if err != nil {
					return err
				}
				defer check.Err(resp.Body.Close)

				if resp.StatusCode == http.StatusBadGateway {
					return fmt.Errorf("server: temporary error")
				} else if resp.StatusCode >= 300 {
					b, _ := ioutil.ReadAll(resp.Body)
					return fmt.Errorf("server: %v", b)
				}

				if err := json.NewDecoder(resp.Body).Decode(&loginResp); err != nil {
					return fmt.Errorf("decoding response: %v", err)
				}

				if err := storeToken(loginResp.Token); err != nil {
					return fmt.Errorf("storing login token: %v", err)
				}

				*authToken = loginResp.Token

				claims := &jwt.StandardClaims{}
				if _, _, err := new(jwt.Parser).ParseUnverified(*authToken, claims); err != nil {
					return fmt.Errorf("parsing authToken: %v", err)
				}

				exp := claims.ExpiresAt
				refreshAt = time.Unix(exp, 0).Add(refreshDurationBuffer)

				return nil
			}
			if err := backoff.RetryNotify(operation,
				backoff.WithContext(backoff.WithMaxRetries(backoff.NewExponentialBackOff(), maxRetries), ctx),
				func(err error, t time.Duration) {
					log.Printf("Token refresh error: %v", err)
					log.Printf("Retrying in %s seconds\n", t.Round(time.Second).String())
				}); err != nil {
				log.Printf("Token refresh error: %v", err)
				return
			}
		}
	}
}
