package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"

	"github.com/go-chi/chi"
	"github.com/google/go-jsonnet"
	"gopkg.in/alecthomas/kingpin.v2"
)

var (
	app        = kingpin.New(filepath.Base(os.Args[0]), "Data Gateway")
	configFile = app.Flag("config", "Configuration file path.").Short('c').
			Default("config.jsonnet").OverrideDefaultFromEnvar("DATAGW_CONFIG").String()
	listenAddress = app.Flag("listen", "Listen address.").Short('l').
			Default(":8080").OverrideDefaultFromEnvar("DATAGW_LISTEN").String()
)

func main() {
	err := run()
	if err != nil {
		log.Println(err)
		os.Exit(1)
	}
}

func run() error {
	app.Version("latest") // TODO(camh): Get version fron git tag
	app.HelpFlag.Short('h')
	app.Parse(os.Args[1:])

	cfg, err := LoadConfigFile(*configFile)
	if err != nil {
		return err
	}

	router := chi.NewRouter()
	router.Get("/", sayHello)
	router.Get("/dump", dumpRequest)
	setupHandlers(router, cfg)
	log.Printf("listening on %s", *listenAddress)
	http.ListenAndServe(*listenAddress, router)

	return nil
}

func sayHello(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintln(w, "Hi there!")
}

func dumpRequest(w http.ResponseWriter, r *http.Request) {
	b, err := marshalRequest(r)
	if err != nil {
		w.WriteHeader(500)
		fmt.Fprintf(w, "%v", err)
		return
	}
	fmt.Fprintf(w, "%s", b)

	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(500)
		fmt.Fprintf(w, "%v", err)
		return
	}
	fmt.Fprintf(w, "\n\n%s\n", body)
}

func LoadConfigFile(filename string) (*Config, error) {
	content, err := ioutil.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	return LoadConfig(string(content), filename)
}

func LoadConfig(s, filename string) (*Config, error) {
	vm := jsonnet.MakeVM()
	output, err := vm.EvaluateSnippet(filename, s)
	if err != nil {
		return nil, err
	}

	var cfg Config
	if err := json.Unmarshal([]byte(output), &cfg); err != nil {
		return nil, fmt.Errorf("could not parse json: %v\n", err)
	}

	return &cfg, nil
}

func setupHandlers(r chi.Router, cfg *Config) {
	for n, h := range cfg.Handlers {
		log.Printf("Adding handler %s", n)
		// NOTE: This panics if h.Method is not valid
		r.MethodFunc(h.Method, h.Path, h.handle)
		r.MethodFunc(h.Method, "/_"+h.Path, h.debugHandle)
	}
}

// Config is the top-level of the data gateway configuration
type Config struct {
	Handlers map[string]Handler `json:"handlers"`
}

type Handler struct {
	Path        string `json:"path"`
	Method      string `json:"method"`
	ContentType string `json:"content_type"`
	Action      string `json:"action"`
}

func (h *Handler) UnmarshalJSON(b []byte) error {
	type plain Handler
	if err := json.Unmarshal(b, (*plain)(h)); err != nil {
		return err
	}
	if h.Path == "" {
		return errors.New("Handler missing 'path' field")
	}
	if h.Action == "" {
		return errors.New("Handler missing 'action' field")
	}
	if h.Method == "" {
		h.Method = "POST"
	}
	// TODO(camh): Check validity of Method and ContentType fields
	return nil
}

// Action represents the outcome of a jsonnet handler
type Action struct {
	HTTP *HTTPAction `json:"http"`
}

type HTTPAction struct {
	Method string      `json:"method"`
	Url    string      `json:"url"`
	Header http.Header `json:"header"`
	Body   string      `json:"body"`
}

func (h *Handler) handle(w http.ResponseWriter, r *http.Request) {
	output, err := h.getAction(r)
	if err != nil {
		w.WriteHeader(500)
		fmt.Fprintf(w, "%v", err)
		return
	}
	var a Action
	if err := json.Unmarshal([]byte(output), &a); err != nil {
		w.WriteHeader(500)
		fmt.Fprintf(w, "%v", err)
		return
	}
	resp, err := a.execute()
	if err != nil {
		w.WriteHeader(500)
		fmt.Fprintf(w, "%v", err)
		return
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

func (h *Handler) debugHandle(w http.ResponseWriter, r *http.Request) {
	output, err := h.getAction(r)
	if err != nil {
		w.WriteHeader(500)
		fmt.Fprintf(w, "%v", err)
		return
	}
	fmt.Fprintf(w, "%s\n", output)
}

func (h *Handler) getAction(r *http.Request) (string, error) {
	vm := jsonnet.MakeVM()
	if err := addNativeFuncs(vm); err != nil {
		return "", err
	}
	if err := h.setExtVars(vm, r); err != nil {
		return "", err
	}
	content, err := ioutil.ReadFile(h.Action)
	if err != nil {
		return "", err
	}
	// It would be nice if we could cache the AST rather than evaluating
	// from source each time.
	output, err := vm.EvaluateSnippet(h.Action, string(content))
	if err != nil {
		return "", err
	}
	return output, nil
}

func (h *Handler) setExtVars(vm *jsonnet.VM, r *http.Request) error {
	ct := r.Header.Get("Content-Type")
	if ct == "" {
		if h.ContentType != "" {
			ct = h.ContentType
		} else {
			ct = "application/octet-stream"
		}
	}

	var body string
	if ct == "application/x-www-form-urlencoded" {
		if err := r.ParseForm(); err != nil {
			return err
		}
	} else {
		b, err := ioutil.ReadAll(r.Body)
		if err != nil {
			return err
		}
		body = string(b)
	}

	b, err := marshalRequest(r)
	if err != nil {
		return err
	}
	vm.ExtCode("request", string(b))

	switch ct {
	case "application/json", "application/x-jsonnet":
		vm.ExtCode("body", body)
	default:
		vm.ExtVar("body", body)
	}
	return nil
}

func marshalRequest(r *http.Request) ([]byte, error) {
	req := struct {
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
		RequestURI       string      `json:"request_uri"`
	}{
		r.Method,
		*r.URL,
		r.URL.Query(),
		r.Proto, r.ProtoMajor, r.ProtoMinor,
		r.Header,
		r.ContentLength,
		r.TransferEncoding,
		r.Host,
		r.RequestURI,
	}

	return json.Marshal(req)
}

func (a *Action) execute() (*http.Response, error) {
	if a.HTTP != nil {
		return a.HTTP.execute()
	}
	return nil, errors.New("no action defined")
}

func (a *HTTPAction) execute() (*http.Response, error) {
	client := &http.Client{}
	req, err := http.NewRequest(a.Method, a.Url, bytes.NewReader([]byte(a.Body)))
	if a.Header != nil {
		req.Header = a.Header
	}
	if err != nil {
		return nil, err
	}
	// TODO(camh): Add basic auth
	// TODO(camh): Add client ssl key/certs
	resp, err := client.Do(req)
	return resp, err
}
