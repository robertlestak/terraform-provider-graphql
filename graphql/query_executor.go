package graphql

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"time"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/logging"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

func exponentialBackoff(attempt int, multiplier int) time.Duration {
	return time.Duration(math.Pow(2, float64(attempt))*float64(multiplier)) * time.Millisecond
}

func queryExecute(ctx context.Context, d *schema.ResourceData, m interface{}, querySource, variableSource string) (*GqlQueryResponse, []byte, error) {
	query := d.Get(querySource).(string)
	inputVariables := d.Get(variableSource).(map[string]interface{})
	apiURL := m.(*graphqlProviderConfig).GQLServerUrl
	headers := m.(*graphqlProviderConfig).RequestHeaders
	authorizationHeaders := m.(*graphqlProviderConfig).RequestAuthorizationHeaders

	var queryBodyBuffer bytes.Buffer

	queryObj := GqlQuery{
		Query:     query,
		Variables: make(map[string]interface{}), // Create an empty map to be populated below
	}

	// Populate GqlQuery variables
	for k, v := range inputVariables {
		// Convert any json string inputs to a struct for complex GraphQL inputs
		js, isJS := isJSON(v)

		if isJS {
			queryObj.Variables[k] = js
		} else {
			// If the input is just a simple string/not JSON
			queryObj.Variables[k] = v
		}
	}

	if err := json.NewEncoder(&queryBodyBuffer).Encode(queryObj); err != nil {
		return nil, nil, err
	}

	maxRetries := d.Get("max_retries").(int)
	retryWaitMs := d.Get("retry_delay").(int)
	retryableStatusCodes := d.Get("retry_status_codes").(*schema.Set).List()
	if len(retryableStatusCodes) == 0 {
		retryableStatusCodes = []interface{}{
			http.StatusTooManyRequests,
			http.StatusServiceUnavailable,
			http.StatusGatewayTimeout,
			http.StatusInternalServerError,
		}
	}
	var resp *http.Response
	var err error
	client := &http.Client{}

	for attempt := 0; attempt <= maxRetries; attempt++ {
		req, err := http.NewRequestWithContext(ctx, "POST", apiURL, &queryBodyBuffer)
		if err != nil {
			return nil, nil, err
		}

		req.Header.Set("Content-Type", "application/json; charset=utf-8")
		req.Header.Set("Accept", "application/json; charset=utf-8")
		for key, value := range authorizationHeaders {
			req.Header.Set(key, value.(string))
		}
		for key, value := range headers {
			req.Header.Set(key, value.(string))
		}

		if logging.IsDebugOrHigher() {
			log.Printf("[DEBUG] Enabling HTTP requests/responses tracing")
			client.Transport = logging.NewTransport("GraphQL", http.DefaultTransport)
		}

		resp, err = client.Do(req)
		if err == nil && !contains(retryableStatusCodes, resp.StatusCode) {
			break
		}

		if attempt < maxRetries {
			waitTime := exponentialBackoff(attempt, retryWaitMs)
			log.Printf("[DEBUG] Retry attempt %d/%d, waiting %s before next attempt", attempt+1, maxRetries, waitTime)
			time.Sleep(waitTime)
		}
	}

	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, err
	}

	var gqlResponse GqlQueryResponse
	if err := json.Unmarshal(body, &gqlResponse); err != nil {
		return nil, nil, fmt.Errorf("unable to parse graphql server response: %v ---> %s", err, string(body))
	}

	return &gqlResponse, body, nil
}

func contains(set []interface{}, item int) bool {
	for _, v := range set {
		if v.(int) == item {
			return true
		}
	}
	return false
}

// isJSON will check if s can be interpreted as valid JSON, and return an unmarshalled struct representing the JSON if it can.
func isJSON(s interface{}) (interface{}, bool) {
	var js interface{}
	err := json.Unmarshal([]byte(s.(string)), &js)
	if err != nil {
		return nil, false
	}
	return js, true
}
