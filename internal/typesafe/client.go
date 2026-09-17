// Package typesafe is a minimal client for the TypeSafe System One API.
//
// The API is a single endpoint: you POST some state plus a map of typed
// questions, and get back one answer per question. The model returns no prose,
// only numbers, so everything downstream is thresholds and sorting.
package typesafe

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	endpoint     = "https://api.typesafe.ai/v1/systemone"
	DefaultModel = "jev-latest"
)

// Key reads the API key. TYPE_SAFE_AI_KEY is the name used in this project's
// setup; the others are what the official SDKs look for, accepted so a machine
// that already has one configured works without extra setup.
func Key() (string, error) {
	for _, name := range []string{"TYPE_SAFE_AI_KEY", "TYPESAFE_API_KEY", "TYPESAFE_AI_API_KEY"} {
		if v := strings.TrimSpace(os.Getenv(name)); v != "" {
			return v, nil
		}
	}
	return "", fmt.Errorf("no API key: set TYPE_SAFE_AI_KEY in your environment")
}

type Client struct {
	key   string
	model string
	http  *http.Client

	// The service allows 1200 requests per minute. One request per file means a
	// large tree can reach that, so requests are spaced rather than left to
	// collide and retry.
	mu          sync.Mutex
	next        time.Time
	minInterval time.Duration

	// Debug writes each raw response body here when non-nil. The field shapes
	// below are written from the published docs; this is how you check them
	// against what the service actually returns.
	Debug io.Writer
}

func New() (*Client, error) {
	k, err := Key()
	if err != nil {
		return nil, err
	}
	model := DefaultModel
	if m := strings.TrimSpace(os.Getenv("JEV_MODEL")); m != "" {
		model = m
	}
	return &Client{
		key:         k,
		model:       model,
		http:        &http.Client{Timeout: 120 * time.Second},
		minInterval: time.Minute / 1000,
	}, nil
}

// --- questions ---------------------------------------------------------

// Question is one typed judgment. Exactly one of the three shapes is valid:
//
//	noul   — probability that a condition holds (no criteria, or an object)
//	choice — pick one of criteria's keys (max 255 options)
//	score  — position along criteria's ordered levels (min 2)
type Question struct {
	Type         string `json:"type"`
	Instructions any    `json:"instructions"`
	Criteria     any    `json:"criteria,omitempty"`
}

func Noul(instructions string) Question {
	return Question{Type: "noul", Instructions: instructions}
}

func Choice(instructions string, criteria map[string]string) Question {
	return Question{Type: "choice", Instructions: instructions, Criteria: criteria}
}

func Score(instructions string, levels []string) Question {
	return Question{Type: "score", Instructions: instructions, Criteria: levels}
}

type Request struct {
	Model     string              `json:"model"`
	State     any                 `json:"state"`
	Questions map[string]Question `json:"questions"`
}

// Answer covers all three primitives. Only the fields belonging to the
// answered type are populated; the rest stay at their zero value. Decoding is
// deliberately tolerant — an unrecognised field is ignored rather than fatal.
type Answer struct {
	Type string `json:"type"`

	Noul float64 `json:"noul"` // noul: probability of yes, 0..1

	Choice string `json:"choice"` // choice: the selected option key

	Score  float64        `json:"score"`  // score: position along the levels
	Legend map[string]any `json:"legend"` // score: levels by number

	Probabilities map[string]float64 `json:"probabilities"` // choice/score
	Confidence    float64            `json:"confidence"`    // choice/score
}

// Value collapses an answer to the single number worth thresholding on.
func (a Answer) Value() float64 {
	if a.Type == "noul" || a.Noul != 0 {
		return a.Noul
	}
	return a.Confidence
}

type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type Response struct {
	Model   string            `json:"model"`
	Answers map[string]Answer `json:"answers"`
	Usage   Usage             `json:"usage"`
}

// --- transport ---------------------------------------------------------

type apiError struct {
	status int
	body   string
}

func (e *apiError) Error() string {
	switch e.status {
	case http.StatusUnauthorized:
		return "401 unauthorized: the API key was rejected (check TYPE_SAFE_AI_KEY)"
	case http.StatusUnprocessableEntity:
		return fmt.Sprintf("422 the request was malformed: %s", e.body)
	case http.StatusServiceUnavailable:
		if strings.Contains(e.body, "model_unavailable") {
			return "503 model_unavailable: TypeSafe reports the model is down. " +
				"This is on their side — the key and the request are fine. Retry later, " +
				"or check status with: curl -H \"Authorization: Bearer $TYPE_SAFE_AI_KEY\" https://api.typesafe.ai/v1/models"
		}
	}
	return fmt.Sprintf("HTTP %d: %s", e.status, e.body)
}

// StatusOf reports the HTTP status behind an error, or 0 if it did not come
// from the API. Callers use it to tailor advice to the actual failure.
func StatusOf(err error) int {
	var ae *apiError
	if errorsAs(err, &ae) {
		return ae.status
	}
	return 0
}

func (e *apiError) retryable() bool {
	return e.status == http.StatusTooManyRequests || e.status >= 500
}

// Ask sends one request and returns its answers. 429 and 5xx are retried with
// exponential backoff and jitter; 401 and 422 fail immediately, since retrying
// a bad key or a bad request only wastes time.
func (c *Client) Ask(ctx context.Context, state any, questions map[string]Question) (*Response, error) {
	if len(questions) == 0 {
		return &Response{Answers: map[string]Answer{}}, nil
	}
	body, err := json.Marshal(Request{Model: c.model, State: state, Questions: questions})
	if err != nil {
		return nil, fmt.Errorf("encoding request: %w", err)
	}

	const maxAttempts = 5
	var lastErr error
	for attempt := range maxAttempts {
		if attempt > 0 {
			backoff := time.Duration(1<<attempt) * 250 * time.Millisecond
			backoff += time.Duration(rand.Int63n(int64(backoff / 2)))
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		resp, err := c.do(ctx, body)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		var ae *apiError
		if !errorsAs(err, &ae) || !ae.retryable() {
			return nil, err
		}
	}
	return nil, fmt.Errorf("gave up after %d attempts: %w", maxAttempts, lastErr)
}

// reserve returns once this caller may send, keeping the global rate under the
// configured ceiling regardless of how many goroutines are in flight.
func (c *Client) reserve(ctx context.Context) error {
	c.mu.Lock()
	now := time.Now()
	if c.next.Before(now) {
		c.next = now
	}
	wait := c.next.Sub(now)
	c.next = c.next.Add(c.minInterval)
	c.mu.Unlock()

	if wait <= 0 {
		return nil
	}
	select {
	case <-time.After(wait):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *Client) do(ctx context.Context, body []byte) (*Response, error) {
	if err := c.reserve(ctx); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("Content-Type", "application/json")

	httpResp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("calling TypeSafe: %w", err)
	}
	defer httpResp.Body.Close()

	raw, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}
	if c.Debug != nil {
		fmt.Fprintf(c.Debug, "--- POST %s -> %d ---\n%s\n", endpoint, httpResp.StatusCode, raw)
	}
	if httpResp.StatusCode != http.StatusOK {
		return nil, &apiError{status: httpResp.StatusCode, body: truncate(string(raw), 400)}
	}

	var out Response
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decoding response: %w (body: %s)", err, truncate(string(raw), 200))
	}
	return &out, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// errorsAs is errors.As, kept local so this file has no dependency beyond the
// standard library's encoding and net packages.
func errorsAs(err error, target **apiError) bool {
	for err != nil {
		if ae, ok := err.(*apiError); ok {
			*target = ae
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
