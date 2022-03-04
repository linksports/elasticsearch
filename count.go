package elasticsearch

import (
	"encoding/json"
	"log"
	"strings"
)

func (es *_elasticsearch) Count(index string, query string) (StatusCode, int, error) {
	res, err := es.client.Count(
		es.client.Count.WithIndex(index),
		es.client.Count.WithBody(strings.NewReader(query)),
	)
	defer res.Body.Close()
	if err != nil {
		log.Fatalf("Error getting count: %s", err)
		return StatusRequestError, 0, err
	}
	if res.IsError() {
		log.Fatalf("[%s] Error indexing document", res.Status())

		switch res.StatusCode {
		case 400:
			return StatusBadRequestError, 0, err
		}
		return StatusError, 0, err
	}

	var r map[string]interface{}
	if err := json.NewDecoder(res.Body).Decode(&r); err != nil {
		log.Fatalf("Error parsing the response body: %s", err)
		return StatusParseError, 0, err
	}

	log.Printf("[%s] %s", res.Status(), r["count"])
	count := r["count"].(float64)

	return StatusSuccess, int(count), nil
}
