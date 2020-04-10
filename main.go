package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"mime"
	"net/http"
	"net/url"

	"github.com/alecthomas/kong"
	"github.com/google/go-jsonnet"
)

type CLI struct {
	Config     string `short":c" env:"PONY_CONFIG"`
	ListenAddr string `short:"l" default:":8080" env:"PONY_LISTEN"`
}

type serve struct {
	method string
	path   string
	script string
}

func main() {
	cli := CLI{}
	kong.Parse(&cli)
	http.ListenAndServe(cli.ListenAddr, http.HandlerFunc(handle))
}

func getContentType(r *http.Request) (string, error) {
	ct := r.Header.Get("Content-Type")
	if ct == "" {
		ct = "application/octet-stream"
	}
	ct, _, err := mime.ParseMediaType(ct)
	if err != nil {
		return "", err
	}
	return ct, nil
}

func handle(w http.ResponseWriter, r *http.Request) {
	// Handling a request involves:
	// 1. Creating a jsonnet VM
	// 2. Creating a request extVar from the http.Request
	// 3. Creating a body extVar from the parsed body (based on Content-Type)
	// 4.
	vm := jsonnet.MakeVM()
	ct, err := getContentType(r)
	if err != nil {
		log.Print(err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	vm.ExtCode("body", body)

	var body string
	switch ct {
	case "application/x-www-form-urlencoded":
		if err := r.ParseForm(); err != nil {
			log.Print(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
	case "multipart/form-data":
		if err := r.ParseMultipartForm(1024 * 1024); err != nil {
			log.Print(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
	default:
		b, err := ioutil.ReadAll(r.Body)
		if err != nil {
			log.Print(err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		body = string(b)
	}
	b, err := marshalRequest(r)
	if err != nil {
		log.Print(err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	var body string
	fmt.Println(string(b))
}

type Request struct {
	Method           string      `json:"method"`
	URL              url.URL     `json:"url"`
	QueryParams      url.Values  `json:"query_params"`
	Proto            string      `json:"proto"`
	ProtoMajor       int         `json:"proto_major"`
	ProtoMinor       int         `json:"proto_minor"`
	Header           http.Header `json:"header"`
	ContentLength    int64       `json:"content_length"`
	TransferEncoding []string    `json:"transfer_encoding"`
	Host             string      `json:"host"`
	PostForm         url.Values  `json:"post_form"`
	RemoteAddr       string      `json:"remote_addr"`
	RequestURI       string      `json:"request_uri"`
}

func marshalRequest(r *http.Request) ([]byte, error) {
	req := Request{
		r.Method,
		*r.URL,
		r.URL.Query(),
		r.Proto, r.ProtoMajor, r.ProtoMinor,
		r.Header,
		r.ContentLength,
		append([]string{}, r.TransferEncoding...),
		r.Host,
		r.PostForm,
		r.RemoteAddr,
		r.RequestURI,
	}

	return json.Marshal(req)
}
