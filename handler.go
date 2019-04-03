package deadmod

import (
	"fmt"
	"github.com/satori/go.uuid"
	"io"
	"net/http"
	"strconv"
	"strings"
)

type StatusCoder interface {
	StatusCode() int
}

type NotFound struct{}

func (n NotFound) StatusCode() int {
	return http.StatusNotFound
}

func (n NotFound) Error() string {
	return "The requested resource could not be found"
}

type MethodNotAllowed []string

func (m MethodNotAllowed) StatusCode() int {
	return http.StatusMethodNotAllowed
}

func (m MethodNotAllowed) Error() string {
	return fmt.Sprintf("Method not allowed. Supported methods: %v", m)
}

func HandleHTTP(rw http.ResponseWriter, rq *http.Request) {
	var err error = NotFound{}
	segments := strings.Split(rq.URL.Path, "/")
	length := len(segments)
	if length >= 1 && segments[0] == "triggers" {
		switch length {
		case 1:
			if rq.Method == "POST" {
				err = createTrigger(rq.Body, rw)
			} else {
				err = MethodNotAllowed{"POST"}
			}
		case 2:
			if id, e2 := uuid.FromString(segments[1]); e2 != nil {
				switch rq.Method {
				case "GET":
					err = getTrigger(id, rw)
				case "DELETE":
					err = deleteTrigger(id, rw)
				default:
					err = MethodNotAllowed{"GET", "DELETE"}
				}
			} // else not found - not a valid UUID
		case 3:
			if id, e2 := uuid.FromString(segments[1]); e2 == nil && segments[2] == "checkin" {
				if rq.Method == "POST" {
					err = checkinTrigger(id, rw)
				} else {
					err = MethodNotAllowed{"POST"}
				}
			} // else not found - not a valid UUID
		}

	}

	if err != nil {
		code := http.StatusInternalServerError
		text := []byte(err.Error())
		if sc, ok := err.(StatusCoder); ok {
			code = sc.StatusCode()
		}

		rw.Header().Add("Content-Type", "text/plain;charset=UTF-8")
		rw.Header().Add("Content-Length", strconv.Itoa(len(text)))
		rw.WriteHeader(code)
		rw.Write(text)
	}
}

func checkinTrigger(uuids uuid.UUID, writer http.ResponseWriter) error {
	writer.Write([]byte("CheckinTrigger"))
	return nil
}

func deleteTrigger(uuids uuid.UUID, writer http.ResponseWriter) error {
	writer.Write([]byte("DeleteTrigger"))
	return nil
}

func getTrigger(uuids uuid.UUID, writer http.ResponseWriter) error {
	writer.Write([]byte("GetTrigger"))
	return nil
}

func createTrigger(body io.ReadCloser, responseWriter http.ResponseWriter) error {
	responseWriter.Write([]byte("CreateTrigger"))
	return nil
}
