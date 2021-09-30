package elasticsearch

import (
	"fmt"
	"os"
	"testing"

	"github.com/bxcodec/faker/v3"
	"github.com/joho/godotenv"
	"github.com/stretchr/testify/assert"
)

const indexName = "test-es-index"

type DocBody struct {
	Id string `json:"id"`
	S  string `json:"s"`
	I  int    `json:"i"`
	B  bool   `json:"b"`
}

func TestMain(m *testing.M) {
	godotenv.Load()

	es := newElasticsearch()
	defer es.DeleteIndeces(indexName)

	os.Exit(m.Run())
}

func newElasticsearch() Elasticsearch {
	return New(&Config{
		Address: []string{
			fmt.Sprintf("http://127.0.0.1:%s", os.Getenv("PORT")),
		},
	})
}

func TestRefresh(t *testing.T) {
	es := newElasticsearch()

	err := es.Refresh()
	assert.NoError(t, err)
}

func TestCreateDocument(t *testing.T) {
	es := newElasticsearch()

	t.Run("Success", func(t *testing.T) {
		var body DocBody
		faker.FakeData(&body)
		body.Id = faker.UUIDDigit()

		status, err := es.CreateDocument(&Document{
			Index: indexName,
			ID:    body.Id,
			Body:  body,
		})
		assert.NoError(t, err)
		assert.Equal(t, StatusCreated, status)
	})

	t.Run("Failure", func(t *testing.T) {
		t.Run("Index is blank", func(t *testing.T) {
			var body DocBody
			faker.FakeData(&body)
			body.Id = faker.UUIDDigit()

			status, err := es.CreateDocument(&Document{
				Index: "",
				ID:    body.Id,
				Body:  body,
			})
			assert.Error(t, err)
			assert.Equal(t, StatusBadRequestError, status)
		})

		t.Run("Body is not json", func(t *testing.T) {
			var body DocBody
			faker.FakeData(&body)
			body.Id = faker.UUIDDigit()

			status, err := es.CreateDocument(&Document{
				Index: indexName,
				ID:    body.Id,
				Body:  1,
			})
			assert.Error(t, err)
			assert.Equal(t, StatusBadRequestError, status)
		})

		t.Run("Body is nil", func(t *testing.T) {
			var body DocBody
			faker.FakeData(&body)
			body.Id = faker.UUIDDigit()

			status, err := es.CreateDocument(&Document{
				Index: indexName,
				ID:    body.Id,
				Body:  nil,
			})
			assert.Error(t, err)
			assert.Equal(t, StatusInternalError, status)
		})
	})
}

func TestUpdateDocument(t *testing.T) {
	es := newElasticsearch()

	type UpdateDoc struct {
		Doc DocBody `json:"doc"`
	}

	var data DocBody
	faker.FakeData(&data)
	data.Id = faker.UUIDDigit()

	es.CreateDocument(&Document{
		Index: indexName,
		ID:    data.Id,
		Body:  data,
	})
	es.Refresh()

	t.Run("Success", func(t *testing.T) {
		var body DocBody
		faker.FakeData(&body)
		body.Id = data.Id
		status, err := es.UpdateDocument(&Document{
			Index: indexName,
			ID:    data.Id,
			Body:  body,
		})

		assert.NoError(t, err)
		assert.Equal(t, StatusSuccess, status)

		es.Refresh()

		var list []DocBody
		status, err = es.Search(indexName, fmt.Sprintf(`{
			"query": {
				"term": {
					"id": {
						"value": "%s"
					}
				}
			}
		}`, data.Id), &list)

		assert.NoError(t, err)
		assert.Equal(t, StatusSuccess, status)
		assert.Equal(t, body.Id, list[0].Id)
		assert.Equal(t, body.S, list[0].S)
		assert.Equal(t, body.B, list[0].B)
		assert.Equal(t, body.I, list[0].I)
	})
}

func TestRemoveDocument(t *testing.T) {
	es := newElasticsearch()

	t.Run("Success", func(t *testing.T) {
		var data DocBody
		faker.FakeData(&data)
		data.Id = faker.UUIDDigit()

		es.CreateDocument(&Document{
			Index: indexName,
			ID:    data.Id,
			Body:  data,
		})
		es.Refresh()

		status, err := es.RemoveDocument(&Document{
			Index: indexName,
			ID:    data.Id,
		})

		assert.NoError(t, err)
		assert.Equal(t, StatusSuccess, status)
	})

	t.Run("Failure", func(t *testing.T) {
		t.Run("Not exists", func(t *testing.T) {
			var data DocBody
			faker.FakeData(&data)
			data.Id = faker.UUIDDigit()

			status, err := es.RemoveDocument(&Document{
				Index: indexName,
				ID:    data.Id,
			})

			assert.Error(t, err)
			assert.Equal(t, StatusNotFoundError, status)
		})
	})
}

func TestSearch(t *testing.T) {
	es := newElasticsearch()

	var data DocBody
	faker.FakeData(&data)
	data.Id = faker.UUIDDigit()

	es.CreateDocument(&Document{
		Index: indexName,
		ID:    data.Id,
		Body:  data,
	})
	es.Refresh()

	t.Run("Found", func(t *testing.T) {
		var list []DocBody
		status, err := es.Search(indexName, fmt.Sprintf(`{
			"query": {
				"term": {
					"id": {
						"value": "%s"
					}
				}
			}
		}`, data.Id), &list)

		assert.NoError(t, err)
		assert.Equal(t, StatusSuccess, status)
		assert.Equal(t, data.Id, list[0].Id)
		assert.Equal(t, data.S, list[0].S)
		assert.Equal(t, data.B, list[0].B)
		assert.Equal(t, data.I, list[0].I)
	})

	t.Run("Not Found", func(t *testing.T) {
		var list []DocBody
		status, err := es.Search(indexName, fmt.Sprintf(`{
			"query": {
				"term": {
					"id": {
						"value": "%s"
					}
				}
			}
		}`, "not-exists"), &list)

		assert.NoError(t, err)
		assert.Equal(t, StatusSuccess, status)
		assert.Empty(t, list)
	})

	t.Run("Multiple", func(t *testing.T) {
		data := make([]DocBody, 3)
		for i, _ := range data {
			var d DocBody
			faker.FakeData(&d)
			d.Id = faker.UUIDDigit()

			es.CreateDocument(&Document{
				Index: indexName,
				ID:    d.Id,
				Body:  d,
			})
			data[i] = d
		}
		es.Refresh()

		var list []DocBody
		status, err := es.Search(indexName, fmt.Sprintf(`{
			"query": {
				"terms": {
					"id": [
						"%s","%s","%s"
					]
				}
			}
		}`, data[0].Id, data[1].Id, data[2].Id), &list)

		assert.NoError(t, err)

		for i, d := range data {
			assert.Equal(t, StatusSuccess, status)
			assert.Equal(t, d.Id, list[i].Id)
			assert.Equal(t, d.S, list[i].S)
			assert.Equal(t, d.B, list[i].B)
			assert.Equal(t, d.I, list[i].I)
		}
	})

	t.Run("Sort", func(t *testing.T) {
		data := make([]DocBody, 3)
		for i, _ := range data {
			var d DocBody
			faker.FakeData(&d)
			d.Id = faker.UUIDDigit()
			d.I = i

			es.CreateDocument(&Document{
				Index: indexName,
				ID:    d.Id,
				Body:  d,
			})
			data[i] = d
		}
		es.Refresh()

		var list []DocBody
		status, err := es.Search(indexName, fmt.Sprintf(`{
			"query": {
				"terms": {
					"id": [
						"%s","%s","%s"
					]
				}
			},
			"sort": [
			  {
				"i": {
					"order": "desc"
				}
			  }
			]
		}`, data[0].Id, data[1].Id, data[2].Id), &list)

		assert.NoError(t, err)

		for index, d := range data {
			i := len(list) - 1 - index
			assert.Equal(t, StatusSuccess, status)
			assert.Equal(t, d.Id, list[i].Id)
			assert.Equal(t, d.S, list[i].S)
			assert.Equal(t, d.B, list[i].B)
			assert.Equal(t, d.I, list[i].I)
		}
	})
}
