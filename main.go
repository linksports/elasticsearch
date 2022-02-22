package elasticsearch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"

	goElasticsearch "github.com/elastic/go-elasticsearch/v7"
	"github.com/elastic/go-elasticsearch/v7/esapi"
)

type StatusCode int

const (
	StatusSuccess         StatusCode = 200
	StatusNoContent       StatusCode = 204
	StatusCreated         StatusCode = 201
	StatusBadRequestError StatusCode = 400
	StatusNotFoundError   StatusCode = 404
	StatusRequestError    StatusCode = 499
	StatusInternalError   StatusCode = 500
	StatusUnexpectedError StatusCode = 520
	StatusParseError      StatusCode = 521
	StatusError           StatusCode = 599
)

type Config struct {
	Address []string
	CloudID string
	APIKey  string
}

type Document struct {
	Index string
	ID    string
	Body  interface{}
}

// for UpdateRequest
type documentBody struct {
	Doc interface{} `json:"doc"`
}

type HitData struct {
	Index string        `json:"_index"`
	Type  string        `json:"_type"`
	Id    string        `json:"_id"`
	Score float64       `json:"_score"`
	Sort  []interface{} `json:"sort"`
}

type AggregationBucket struct {
	Key      interface{} `json:"key"`
	DocCount float64     `json:"doc_count"`
}

type Elasticsearch interface {
	Refresh() error
	Ping() error
	CreateIndexTemplate(name, templates string) (StatusCode, error)
	CreateDocument(doc *Document) (StatusCode, error)
	UpdateDocument(doc *Document) (StatusCode, error)
	RemoveDocument(doc *Document) (StatusCode, error)
	Search(index string, query string, data interface{}) (StatusCode, []*HitData, int, error)
	SearchWithAggs(index string, query string, data interface{}, aggsKey []string) (StatusCode, []*HitData, int, map[string][]*AggregationBucket, error)

	DeleteIndeces(index ...string) (StatusCode, error)
}

func New(config *Config) Elasticsearch {
	return &_elasticsearch{client: connectElasticsearch(config)}
}

func (es *_elasticsearch) Ping() error {
	_, err := es.client.Ping()
	return err
}

func (es *_elasticsearch) CreateIndexTemplate(name, templates string) (StatusCode, error) {
	req := esapi.IndicesPutIndexTemplateRequest{
		Body: strings.NewReader(templates),
		Name: name,
	}

	res, err := req.Do(context.Background(), es.client)

	if err != nil {
		return StatusInternalError, err
	}

	defer res.Body.Close()

	if res.IsError() {
		log.Printf("[%s] Error Create Index Template %s", res.Status(), templates)
		switch res.StatusCode {
		case 400:
			return StatusBadRequestError, errors.New("bad request")
		}
		return StatusError, err
	}

	return StatusSuccess, err
}

func (es *_elasticsearch) Refresh() error {
	_, err := es.client.Indices.Refresh()
	return err
}

func (es *_elasticsearch) CreateDocument(doc *Document) (StatusCode, error) {
	if doc.Body == nil {
		return StatusInternalError, errors.New("Required body")
	}

	body, err := json.Marshal(doc.Body)
	if err != nil {
		return StatusInternalError, err
	}

	req := esapi.IndexRequest{
		Index:      doc.Index,
		DocumentID: doc.ID,
		Body:       bytes.NewReader(body),
	}

	res, err := req.Do(context.Background(), es.client)
	if err != nil {
		log.Printf("Error getting response: %s", err)
		return StatusRequestError, err
	}
	defer res.Body.Close()

	if res.IsError() {
		log.Printf("[%s] Error indexing doc ID=%s", res.Status(), doc.ID)
		switch res.StatusCode {
		case 400:
			return StatusBadRequestError, errors.New("bad request")
		}
		return StatusError, err
	} else {
		// Deserialize the response into a map.
		var r map[string]interface{}
		if err := json.NewDecoder(res.Body).Decode(&r); err != nil {
			log.Printf("Error parsing the response body: %s", err)
			return StatusUnexpectedError, nil
		} else {
			log.Printf("[%s] %s; version=%d ; id=%s", res.Status(), r["result"], int(r["_version"].(float64)), r["_id"])
		}
	}

	return StatusCreated, err
}

func (es *_elasticsearch) UpdateDocument(doc *Document) (StatusCode, error) {
	if doc.Body == nil {
		return StatusInternalError, errors.New("Required body")
	}

	body, err := json.Marshal(&documentBody{
		Doc: doc.Body, // https://discuss.elastic.co/t/updating-elasticsearch-document/265705
	})
	if err != nil {
		return StatusInternalError, err
	}

	req := esapi.UpdateRequest{
		Index:      doc.Index,
		DocumentID: doc.ID,
		Body:       bytes.NewReader(body),
	}

	res, err := req.Do(context.Background(), es.client)
	if err != nil {
		log.Printf("Error getting response: %s", err)
		return StatusRequestError, err
	}
	defer res.Body.Close()

	if res.IsError() {
		log.Printf("[%s] Error indexing doc ID=%s : %s", res.Status(), doc.ID, res.String())
		switch res.StatusCode {
		case 400:
			return StatusBadRequestError, errors.New("bad request")
		}
		return StatusError, err
	} else {
		// Deserialize the response into a map.
		var r map[string]interface{}
		if err := json.NewDecoder(res.Body).Decode(&r); err != nil {
			log.Printf("Error parsing the response body: %s", err)
			return StatusUnexpectedError, err
		} else {
			log.Printf("[%s] %s; version=%d ; id=%s", res.Status(), r["result"], int(r["_version"].(float64)), r["_id"])
		}
	}
	return StatusSuccess, err
}

func (es *_elasticsearch) RemoveDocument(doc *Document) (StatusCode, error) {
	req := esapi.DeleteRequest{
		Index:      doc.Index,
		DocumentID: doc.ID,
	}

	res, err := req.Do(context.Background(), es.client)
	if err != nil {
		log.Printf("Error getting response: %s", err)
		return StatusRequestError, err
	}
	if res.IsError() {
		log.Printf("[%s] Error indexing doc ID=%s", res.Status(), doc.Index)
		switch res.StatusCode {
		case 400:
			return StatusBadRequestError, errors.New("bad request")
		case 404:
			return StatusNotFoundError, errors.New("not found")
		}
		return StatusError, err
	} else {
		// Deserialize the response into a map.
		var r map[string]interface{}
		if err := json.NewDecoder(res.Body).Decode(&r); err != nil {
			log.Printf("Error parsing the response body: %s", err)
			return StatusUnexpectedError, errors.New("parse error")
		}
	}

	return StatusSuccess, err
}

func (es *_elasticsearch) Search(index string, query string, data interface{}) (StatusCode, []*HitData, int, error) {

	searchStatusCode, result, err := es.search(index, query)

	if searchStatusCode != StatusSuccess {
		return searchStatusCode, []*HitData{}, 0, err
	}

	parseStatusCode, total, sources, hitsData, err := parseHits(result)

	if parseStatusCode != StatusSuccess {
		return parseStatusCode, []*HitData{}, 0, err
	}

	tmp, err := json.Marshal(sources)
	if err := json.Unmarshal(tmp, data); err != nil {
		return StatusParseError, []*HitData{}, 0, err
	}

	return StatusSuccess, hitsData, total, nil
}

func (es *_elasticsearch) SearchWithAggs(index string, query string, data interface{}, aggsKey []string) (StatusCode, []*HitData, int, map[string][]*AggregationBucket, error) {

	searchStatusCode, result, err := es.search(index, query)
	aggs := map[string][]*AggregationBucket{}
	if searchStatusCode != StatusSuccess {
		return searchStatusCode, []*HitData{}, 0, aggs, err
	}

	parseStatusCode, total, sources, hitsData, err := parseHits(result)

	if parseStatusCode != StatusSuccess {
		return parseStatusCode, []*HitData{}, 0, aggs, err
	}

	tmp, err := json.Marshal(sources)
	if err := json.Unmarshal(tmp, data); err != nil {
		return StatusParseError, []*HitData{}, 0, aggs, err
	}

	if len(aggsKey) != 0 {
		if _, ok := result["aggregations"]; !ok {
			return StatusNoContent, []*HitData{}, 0, aggs, err
		}
		for _, key := range aggsKey {
			buckets := result["aggregations"].(map[string]interface{})[key].(map[string]interface{})["buckets"].([]interface{})
			var aggregationBucket []*AggregationBucket

			for _, bucket := range buckets {
				agg := AggregationBucket{
					Key:      bucket.(map[string]interface{})["key"],
					DocCount: bucket.(map[string]interface{})["doc_count"].(float64),
				}

				aggregationBucket = append(aggregationBucket, &agg)
			}

			aggs[key] = aggregationBucket
		}

	}

	return StatusSuccess, hitsData, total, aggs, nil

}

func (es *_elasticsearch) search(index string, query string) (statusCode StatusCode, result map[string]interface{}, err error) {
	// Perform the search request.
	res, err := es.client.Search(
		es.client.Search.WithContext(context.Background()),
		es.client.Search.WithIndex(index),
		es.client.Search.WithBody(strings.NewReader(query)),
		es.client.Search.WithTrackTotalHits(true),
		es.client.Search.WithPretty(),
	)

	if err != nil {
		log.Fatalf("Error getting response: %s", err)
		return StatusRequestError, nil, err
	}
	defer res.Body.Close()

	if res.IsError() {
		var e map[string]interface{}
		if err := json.NewDecoder(res.Body).Decode(&e); err != nil {
			log.Fatalf("Error parsing the response body: %s", err)
		} else {
			//Print the response status and error information.
			log.Fatalf("[%s] %s: %s",
				res.Status(),
				e["error"].(map[string]interface{})["type"],
				e["error"].(map[string]interface{})["reason"],
			)
		}

		switch res.StatusCode {
		case 400:
			return StatusBadRequestError, result, err
		}
		return StatusError, nil, err
	}

	if err := json.NewDecoder(res.Body).Decode(&result); err != nil {
		return StatusParseError, nil, err
	}
	return StatusSuccess, result, nil
}

func parseHits(result map[string]interface{}) (statusCode StatusCode, total int, sources []interface{}, hitsData []*HitData, err error) {
	// Hits
	if _, ok := result["hits"]; !ok {
		return StatusNoContent, 0, nil, []*HitData{}, nil
	}

	if t, existsTotal := result["hits"].(map[string]interface{})["total"]; existsTotal {
		total = int(t.(map[string]interface{})["value"].(float64))
	}

	hits := result["hits"].(map[string]interface{})["hits"].([]interface{})

	sources = make([]interface{}, len(hits))
	hitsData = make([]*HitData, len(hits))

	for i, hit := range hits {
		sources[i] = hit.(map[string]interface{})["_source"]

		h := &HitData{
			Index: hit.(map[string]interface{})["_index"].(string),
			Type:  hit.(map[string]interface{})["_type"].(string),
			Id:    hit.(map[string]interface{})["_id"].(string),
		}

		if score, _ := hit.(map[string]interface{})["_score"]; score != nil {
			h.Score = score.(float64)
		}

		if sort, _ := hit.(map[string]interface{})["sort"]; sort != nil {
			h.Sort = sort.([]interface{})
		}

		hitsData[i] = h
	}

	return StatusSuccess, total, sources, hitsData, nil

}

func (es *_elasticsearch) DeleteIndeces(index ...string) (StatusCode, error) {

	req := esapi.IndicesDeleteRequest{
		Index: index,
	}
	res, err := req.Do(context.Background(), es.client)
	if res.IsError() {
		return StatusUnexpectedError, err
	}

	return StatusSuccess, err
}

type _elasticsearch struct {
	client *goElasticsearch.Client
}

func connectElasticsearch(config *Config) *goElasticsearch.Client {

	cfg := goElasticsearch.Config{
		Addresses: config.Address,
		CloudID:   config.CloudID,
		APIKey:    config.APIKey,
	}
	client, err := goElasticsearch.NewClient(cfg)

	if err != nil {
		fmt.Printf("Error New: %s", err)

	}

	return client
}

func refresh2string(r *bool) string {
	if r != nil {
		return map[bool]string{true: "true", false: "false"}[*r]
	}
	return "false"
}
