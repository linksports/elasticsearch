package elasticsearch

import (
	"encoding/json"
	"io"
	"log"
)

func (es *_elasticsearch) GetSource(index string, id string, result any) (int, error) {
	res, err := es.client.GetSource(index, id)
	if err != nil {
		log.Fatalf("Error getting response: %s", err)
		return res.StatusCode, err
	}

	defer res.Body.Close()

	if res.StatusCode == 404 {
		return res.StatusCode, nil
	}

	body, err := io.ReadAll(res.Body)
	if err != nil {
		log.Fatalf("Error reading response: %s", err)
		return res.StatusCode, err
	}

	err = json.Unmarshal(body, result)
	if err != nil {
		log.Fatalf("Error parsing response: %s", err)
		return res.StatusCode, err
	}

	return res.StatusCode, nil
}
