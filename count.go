package elasticsearch

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
)

func (es *_elasticsearch) Count(index string, query string) (StatusCode, int, error) {
	res, err := es.client.Count(
		es.client.Count.WithIndex(index),
		es.client.Count.WithBody(strings.NewReader(query)),
	)
	if err != nil {
		log.Fatalf("Error getting count: %s", err)
		return StatusRequestError, 0, err
	}

	defer res.Body.Close()

	if res.IsError() {
		err := errors.New(fmt.Sprintf("[%s] Error indexing document", res.Status()))

		switch res.StatusCode {
		case 400:
			return StatusBadRequestError, 0, err
		case 404:
			return StatusNotFoundError, 0, err
		}
		return StatusError, 0, err
	}

	var r map[string]any
	if err := json.NewDecoder(res.Body).Decode(&r); err != nil {
		log.Fatalf("Error parsing the response body: %s", err)
		return StatusParseError, 0, err
	}

	log.Printf("[%s] %s", res.Status(), r["count"])
	count := r["count"].(float64)

	return StatusSuccess, int(count), nil
}
