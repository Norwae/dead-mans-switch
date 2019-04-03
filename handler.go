package deadmod

import (
	"cloud.google.com/go/datastore"
	"context"
	"encoding/json"
	"fmt"
	"github.com/satori/go.uuid"
	"google.golang.org/api/iterator"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// datastore setup and initialization

var (
	store    *datastore.Client
	baseURL  string
	notFound = StatusCodeError{http.StatusNotFound, "The requested URL could not be found"}
)

func init() {
	// unfortunately, there seems to be no simple env property reporting the unmangled invocations URL, so
	// we build it from its parts

	projectId := os.Getenv("GCP_PROJECT")
	region := os.Getenv("FUNCTION_REGION")
	name := os.Getenv("FUNCTION_NAME")

	baseURL = fmt.Sprint("https://", region, "-", projectId, ".cloudfunctions.net/", name)

	client, err := datastore.NewClient(context.Background(), projectId)
	if err != nil {
		log.Fatalf("Could not create datastore client: %v", err)
	} else {
		log.Printf("Initialized datastore client for project %s with base url %s", projectId, baseURL)
		store = client
	}
}

// http layer

type StatusCodeError struct {
	StatusCode int
	Message    string
}

func (ue *StatusCodeError) Error() string {
	return ue.Message
}

func HandleHTTP(rw http.ResponseWriter, rq *http.Request) {
	var err error = &notFound
	segments := strings.Split(rq.URL.Path, "/")
	length := len(segments)
	log.Printf("Split path into %d segments: %v", length, segments)
	if length >= 2 && segments[1] == "triggers" {
		switch length {
		case 2:
			if rq.Method == "POST" {
				err = createTrigger(rq.Context(), rq.Body, rw)
			} else {
				err = &StatusCodeError{http.StatusMethodNotAllowed, "The requested method is not available. Available methods: POST"}
			}
		case 3:
			if id, e2 := uuid.FromString(segments[2]); e2 == nil {
				switch rq.Method {
				case "GET":
					err = getTrigger(rq.Context(), id, rw)
				case "DELETE":
					err = deleteTrigger(rq.Context(), id, rw)
				default:

					err = &StatusCodeError{http.StatusMethodNotAllowed, "The requested method is not available. Available methods: GET, DELETE"}
				}
			} else {
				log.Printf("Could not parse %s into a valid UUID: %s", segments[2], e2)

				// not found - not a valid UUID
			}
		case 4:
			if id, e2 := uuid.FromString(segments[2]); e2 == nil && segments[3] == "checkin" {
				if rq.Method == "POST" {
					err = checkinTrigger(rq.Context(), id, rw)
				} else {
					err = &StatusCodeError{http.StatusMethodNotAllowed, "The requested method is not available. Available methods: POST"}
				}
			} else {
				log.Printf("Could not parse %s into a valid UUID: %s", segments[2], e2)
				// not found - not a valid UUID
			}
		}

	}

	if err != nil {
		code := http.StatusInternalServerError
		text := []byte(err.Error())
		if sc, ok := err.(*StatusCodeError); ok {
			code = sc.StatusCode
		}
		log.Printf("Error detected, reporting status %v to user (%v)", code, err)

		rw.Header().Add("Content-Type", "text/plain;charset=UTF-8")
		rw.Header().Add("Content-Length", strconv.Itoa(len(text)))
		rw.WriteHeader(code)
		_, _ = rw.Write(text)
	}
}

// service logic

const Kind = "DMT"

type DeadMansTrigger struct {
	Id               string      `json:"id" datastore:",noindex"`
	DueToFire        time.Time   `json:"due"`
	HoursBetweenFire int         `json:"hoursBetweenFire" datastore:",noindex"`
	Checkins         []time.Time `json:"checkins" datastore:",noindex"`
	FireURL          string      `json:"fireURL" datastore:",noindex"`
	FirePayload      string      `json:"firePayload" datastore:",noindex"`
}

func checkinTrigger(ctx context.Context, id uuid.UUID, writer http.ResponseWriter) error {
	entity := DeadMansTrigger{}
	key := datastore.Key{Kind: Kind, Name: id.String()}
	err := store.Get(ctx, &key, &entity)

	if err == nil {
		now := time.Now().Truncate(time.Second)
		entity.Checkins = append(entity.Checkins, now)
		entity.DueToFire = now.Add(time.Duration(entity.HoursBetweenFire * int(time.Hour)))

		_, err = store.Put(ctx, &key, &entity)

		if err == nil {
			sendEntity(writer, &entity)
		}
	} else if err == datastore.ErrNoSuchEntity {
		err = &notFound
	}

	return err
}

func deleteTrigger(ctx context.Context, id uuid.UUID, writer http.ResponseWriter) error {
	log.Printf("Deleting trigger for %v from datastore", id)
	err := store.Delete(ctx, &datastore.Key{Kind: Kind, Name: id.String()})
	if err == nil {
		writer.WriteHeader(http.StatusNoContent)
	}
	return err
}

func getTrigger(ctx context.Context, id uuid.UUID, writer http.ResponseWriter) error {
	entity := DeadMansTrigger{}
	err := store.Get(ctx, &datastore.Key{Kind: Kind, Name: id.String()}, &entity)

	if err == nil {
		sendEntity(writer, &entity)
	} else if err == datastore.ErrNoSuchEntity {
		err = &notFound
	}

	return err
}

func sendEntity(writer http.ResponseWriter, entity *DeadMansTrigger) {
	writer.Header().Add("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(&entity)
}

func createTrigger(ctx context.Context, body io.ReadCloser, responseWriter http.ResponseWriter) error {
	var input struct {
		HoursBetweenFire int    `json:"hoursBetweenFire"`
		FireURL          string `json:"fireURL"`
		FirePayload      string `json:"firePayload"`
	}

	err := json.NewDecoder(body).Decode(&input)

	if err == nil {
		now := time.Now().Truncate(time.Second)
		fullEntity := DeadMansTrigger{
			Id:               uuid.NewV4().String(),
			DueToFire:        now.Add(time.Duration(input.HoursBetweenFire * int(time.Hour))),
			Checkins:         []time.Time{now},
			HoursBetweenFire: input.HoursBetweenFire,
			FirePayload:      input.FirePayload,
			FireURL:          input.FireURL,
		}

		if _, err = store.Put(ctx, &datastore.Key{Kind: Kind, Name: fullEntity.Id}, &fullEntity); err == nil {
			path := fmt.Sprint(baseURL, "/triggers/", fullEntity.Id)
			responseWriter.Header().Add("Location", path)
			responseWriter.WriteHeader(http.StatusTemporaryRedirect)
			log.Printf("Inserted entity %s, and redirected user to %s", fullEntity.Id, path)
		}
	} else {
		err = &StatusCodeError{http.StatusUnprocessableEntity, err.Error()}
	}

	return err
}

// cronjob logic

type IgnoredParameter struct{}

func ServeCron(ctx context.Context, ignore IgnoredParameter) error {
	var (
		err error
		k   *datastore.Key
		wg  sync.WaitGroup
	)
	query := datastore.NewQuery(Kind).Filter("DueToFire <= ", time.Now())
	it := store.Run(ctx, query)
	target := DeadMansTrigger{}
	for k, err = it.Next(&target); err == nil; _, err = it.Next(&target) {
		log.Printf("Firing %s callback to %s (async)", target.Id, target.FireURL)
		wg.Add(1)
		go fireHttpCallback(ctx, target.FireURL, target.FirePayload, &wg)
		err = store.Delete(ctx, k)
	}

	if err == iterator.Done {
		err = nil
	}

	wg.Wait()

	return err
}

func fireHttpCallback(ctx context.Context, requestUrl string, body string, group *sync.WaitGroup) {
	var rsp *http.Response
	rq, err := http.NewRequest("POST", requestUrl, strings.NewReader(body))
	if err == nil {
		if rsp, err = http.DefaultClient.Do(rq.WithContext(ctx)); err == nil {
			log.Printf("Successfully invoked %s (status code: %d)", requestUrl, rsp.StatusCode)
		}
	}

	if err != nil {
		log.Printf("Failed to invoke %s, error: %v", requestUrl, err)
	}

	group.Done()
}
