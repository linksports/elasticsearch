package elasticsearch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	goElasticsearch "github.com/elastic/go-elasticsearch/v8"
	"github.com/elastic/go-elasticsearch/v8/esapi"
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
	Address       []string
	CloudID       string
	APIKey        string
	MaxRetries    int
	RetryOnStatus []int
	RetryBackoff  func(attempt int) time.Duration
}

// https://www.elastic.co/guide/en/elasticsearch/reference/current/docs-refresh.html
type RefreshPolicy string

const (
	RefreshTrue    RefreshPolicy = "true"
	RefreshFalse   RefreshPolicy = "false"
	RefreshWaitFor RefreshPolicy = "wait_for"
)

type Document struct {
	Index   string
	ID      string
	Body    interface{}
	Refresh RefreshPolicy
}

// for UpdateRequest
type documentBody struct {
	Doc interface{} `json:"doc"`
}

type HitData struct {
	Index string        `json:"_index"`
	Id    string        `json:"_id"`
	Score float64       `json:"_score"`
	Sort  []interface{} `json:"sort"`
}

type AggregationBucket struct {
	Key      interface{} `json:"key"`
	DocCount float64     `json:"doc_count"`
}

type Elasticsearch interface {
	Refresh(index ...string) error
	Ping() error

	CreateIndexTemplate(name, templates string) (StatusCode, error)
	CreateDocument(doc *Document) (StatusCode, error)
	CreateDocuments(docs []*Document) (StatusCode, error)
	UpdateDocument(doc *Document) (StatusCode, error)
	RemoveDocument(doc *Document) (StatusCode, error)

	Search(index string, query string, data interface{}) (StatusCode, []*HitData, int, error)
	SearchWithAggregations(index string, query string, data interface{}, aggsKeys []string) (StatusCode, []*HitData, int, map[string][]*AggregationBucket, error)
	GetSource(index string, id string, result any) (int, error)
	Count(index string, query string) (StatusCode, int, error)

	DeleteByQuery(indices []string, query string) (StatusCode, error)

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

func (es *_elasticsearch) Refresh(index ...string) error {
	_, err := es.client.Indices.Refresh(func(req *esapi.IndicesRefreshRequest) {
		req.Index = index
	})

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
		Refresh:    string(doc.Refresh),
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

func (es *_elasticsearch) CreateDocuments(docs []*Document) (StatusCode, error) {
	if len(docs) == 0 {
		return StatusInternalError, errors.New("required documents")
	}

	var buf bytes.Buffer
	for _, doc := range docs {
		if doc.Body == nil {
			return StatusInternalError, errors.New("one or more documents have nil body")
		}

		meta := fmt.Sprintf(`{ "index": { "_index": "%s", "_id": "%s" } }`, doc.Index, doc.ID)
		buf.WriteString(meta + "\n")

		body, err := json.Marshal(doc.Body)
		if err != nil {
			return StatusInternalError, err
		}
		buf.Write(body)
		buf.WriteString("\n")
	}

	req := esapi.BulkRequest{
		Body:    bytes.NewReader(buf.Bytes()),
		Refresh: string(docs[0].Refresh),
	}

	res, err := req.Do(context.Background(), es.client)
	if err != nil {
		log.Printf("Error getting response: %s", err)
		return StatusRequestError, err
	}
	defer res.Body.Close()

	if res.IsError() {
		log.Printf("[%s] Error indexing docs", res.Status())
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
		}

		if items, ok := r["items"].([]interface{}); ok {
			for i, item := range items {
				if itemMap, ok := item.(map[string]interface{}); ok {
					for _, data := range itemMap {
						if indexData, ok := data.(map[string]interface{}); ok {
							if indexError, ok := indexData["error"].(map[string]interface{}); ok {
								log.Printf("[%d] error=%s,%s; id=%s", int(indexData["status"].(float64)), indexError["type"], indexError["reason"], indexData["_id"])
							} else {
								log.Printf("[%d] %s; version=%d ; id=%s", int(indexData["status"].(float64)), indexData["result"], int(indexData["_version"].(float64)), indexData["_id"])
							}
						} else {
							log.Printf("Item %d: Unexpected data format: %+v", i, data)
						}
					}
				} else {
					log.Printf("Item %d: Invalid format: %+v", i, item)
				}
			}
		} else {
			log.Println("No items found in response")
		}

		if errorsField, ok := r["errors"]; ok && errorsField.(bool) {
			return StatusUnexpectedError, errors.New("operation completed with some errors")
		}
	}

	return StatusCreated, nil
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
		return StatusError, errors.New(res.String())
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
	return StatusSuccess, nil
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
		return StatusError, errors.New(res.String())
	} else {
		// Deserialize the response into a map.
		var r map[string]interface{}
		if err := json.NewDecoder(res.Body).Decode(&r); err != nil {
			log.Printf("Error parsing the response body: %s", err)
			return StatusUnexpectedError, errors.New("parse error")
		}
	}

	return StatusSuccess, nil
}

func (es *_elasticsearch) Search(index string, query string, data interface{}) (StatusCode, []*HitData, int, error) {
	statusCode, result, err := search(es.client, index, query)
	if err != nil {
		return statusCode, []*HitData{}, 0, err
	}

	statusCode, hitsData, total, err := parseHitsData(result, data)
	if err != nil {
		return statusCode, []*HitData{}, 0, err
	}

	return StatusSuccess, hitsData, total, nil
}

func (es *_elasticsearch) SearchWithAggregations(index string, query string, data interface{}, aggsKeys []string) (StatusCode, []*HitData, int, map[string][]*AggregationBucket, error) {
	aggs := map[string][]*AggregationBucket{}
	statusCode, result, err := search(es.client, index, query)
	if err != nil {
		return statusCode, []*HitData{}, 0, aggs, err
	}

	statusCode, hitsData, total, err := parseHitsData(result, data)
	if err != nil {
		return statusCode, []*HitData{}, 0, aggs, err
	}

	if _, ok := result["aggregations"]; !ok {
		return StatusNoContent, []*HitData{}, 0, aggs, nil
	}

	if len(aggsKeys) > 0 {
		for _, key := range aggsKeys {
			if _, ok := result["aggregations"].(map[string]interface{})[key]; !ok {
				continue
			}

			buckets := result["aggregations"].(map[string]interface{})[key].(map[string]interface{})["buckets"].([]interface{})
			var aggregationBucket []*AggregationBucket
			for _, bucket := range buckets {
				b := &AggregationBucket{
					Key:      bucket.(map[string]interface{})["key"],
					DocCount: bucket.(map[string]interface{})["doc_count"].(float64),
				}
				aggregationBucket = append(aggregationBucket, b)
			}

			aggs[key] = aggregationBucket
		}
	}

	return StatusSuccess, hitsData, total, aggs, nil
}

func (es *_elasticsearch) DeleteByQuery(indices []string, query string) (StatusCode, error) {
	res, err := es.client.DeleteByQuery(indices, strings.NewReader(query))

	if err != nil {
		log.Printf("Error getting response: %s indices=%v query=%s", err, indices, query)
		return StatusRequestError, err
	}
	if res.IsError() {
		log.Printf("[%s] Error indices=%v query=%s", res.Status(), indices, query)
		switch res.StatusCode {
		case 400:
			return StatusBadRequestError, errors.New("bad request")
		}
		return StatusError, errors.New(res.String())
	}

	return StatusSuccess, nil

}

func (es *_elasticsearch) DeleteIndeces(index ...string) (StatusCode, error) {

	req := esapi.IndicesDeleteRequest{
		Index: index,
	}
	res, err := req.Do(context.Background(), es.client)
	if err != nil {
		return StatusError, err
	}
	if res.IsError() {
		return StatusUnexpectedError, errors.New(res.String())
	}

	return StatusSuccess, nil
}

type _elasticsearch struct {
	client *goElasticsearch.Client
}

func connectElasticsearch(config *Config) *goElasticsearch.Client {

	cfg := goElasticsearch.Config{
		Addresses:     config.Address,
		CloudID:       config.CloudID,
		APIKey:        config.APIKey,
		MaxRetries:    config.MaxRetries,
		RetryOnStatus: config.RetryOnStatus,
		RetryBackoff:  config.RetryBackoff,
	}
	client, err := goElasticsearch.NewClient(cfg)

	if err != nil {
		fmt.Printf("Error New: %s", err)

	}

	return client
}

func search(client *goElasticsearch.Client, index string, query string) (StatusCode, map[string]interface{}, error) {
	var result map[string]interface{}
	res, err := client.Search(
		client.Search.WithContext(context.Background()),
		client.Search.WithIndex(index),
		client.Search.WithBody(strings.NewReader(query)),
		client.Search.WithTrackTotalHits(true),
		client.Search.WithPretty(),
	)
	if err != nil {
		log.Printf("Error getting response: %s", err)
		return StatusRequestError, result, err
	}
	defer res.Body.Close()

	if res.IsError() {
		var esErr error
		var e map[string]interface{}
		if err := json.NewDecoder(res.Body).Decode(&e); err != nil {
			esErr = fmt.Errorf("error parsing the response body: %s", err)
		} else {
			//Print the response status and error information.
			esErr = fmt.Errorf("[%s] %s: %s",
				res.Status(),
				e["error"].(map[string]interface{})["type"],
				e["error"].(map[string]interface{})["reason"],
			)
		}
		log.Println(esErr)

		switch res.StatusCode {
		case 400:
			return StatusBadRequestError, result, esErr
		}
		return StatusError, result, esErr
	}

	if err := json.NewDecoder(res.Body).Decode(&result); err != nil {
		return StatusParseError, result, err
	}

	return StatusSuccess, result, nil
}

func parseHitsData(result map[string]interface{}, data interface{}) (StatusCode, []*HitData, int, error) {
	if _, ok := result["hits"]; !ok {
		return StatusNoContent, []*HitData{}, 0, nil
	}

	total := 0
	if t, existsTotal := result["hits"].(map[string]interface{})["total"]; existsTotal {
		total = int(t.(map[string]interface{})["value"].(float64))
	}

	hits := result["hits"].(map[string]interface{})["hits"].([]interface{})

	documents := make([]interface{}, len(hits))
	hitsData := make([]*HitData, len(hits))

	for i, hit := range hits {
		documents[i] = hit.(map[string]interface{})["_source"]

		h := &HitData{
			Index: hit.(map[string]interface{})["_index"].(string),
			Id:    hit.(map[string]interface{})["_id"].(string),
		}

		if score := hit.(map[string]interface{})["_score"]; score != nil {
			h.Score = score.(float64)
		}

		if sort := hit.(map[string]interface{})["sort"]; sort != nil {
			h.Sort = sort.([]interface{})
		}

		hitsData[i] = h
	}

	tmp, _ := json.Marshal(documents)
	if err := json.Unmarshal(tmp, data); err != nil {
		return StatusParseError, []*HitData{}, 0, err
	}

	return StatusSuccess, hitsData, total, nil
}
