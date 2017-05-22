package main

import (
	"bytes"
	"flag"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/patrickmn/go-cache"
	"github.com/raff/godet"
)

func main() {
	chromeFlag := flag.String("chrome", "localhost:9222", "Chrome Debugger URL")
	listenFlag := flag.String("listen", "localhost:9444", "Listen interface")

	flag.Parse()

	// create a new cache
	c := cache.New(5*time.Minute, 5*time.Minute)

	// Connect to debugger
	chrome, err := godet.Connect(*chromeFlag, false)
	if err != nil {
		log.Fatalln(err)
	}
	defer chrome.Close()

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		url := r.URL.Query().Get("url")

		// see if we have it cached?
		if out, ok := c.Get(url); ok {
			w.Write(out.([]byte))
			return
		}

		// get fresh html
		out, err := fetchHTML(chrome, url)
		if err != nil {
			w.WriteHeader(500)
			return
		}
		w.Write(out)

		// cache results
		c.Set(url, out, cache.DefaultExpiration)
	})

	log.Printf("Start listening %v ...", *listenFlag)
	http.ListenAndServe(*listenFlag, nil)
}

var fetchHTMLMu sync.Mutex

func fetchHTML(chrome *godet.RemoteDebugger, url string) ([]byte, error) {
	// lock, because can only process one URL at a time
	// TODO: allow parallel processing
	fetchHTMLMu.Lock()
	defer fetchHTMLMu.Unlock()

	// Open new tab and make sure to close it when done
	tab, err := chrome.NewTab(url)
	if err != nil {
		log.Printf("chrome.NewTab: %v", err)
	}
	defer chrome.CloseTab(tab)

	// Enable events
	// Docs: https://chromedevtools.github.io/devtools-protocol/tot/Page/
	if err := chrome.PageEvents(true); err != nil {
		log.Printf("chrome.PageEvents: %v", err)
	}

	log.Printf("New Tab: %v (%v)", tab.URL, tab.ID)

	done := make(chan bool)
	var out bytes.Buffer

	// Create Page.loadEventFired callback
	chrome.CallbackEvent("Page.loadEventFired", func(params godet.Params) {
		// give courtesy delay
		time.Sleep(100 * time.Millisecond)

		// fetch rendered html
		o, err := chrome.GetDocument()
		if err != nil {
			log.Printf("chrome.GetDocument: %v", err)
			return
		}

		rootNodeId := -1
		for n, e := range o {
			if n == "root" {
				if x, ok := e.(map[string]interface{}); ok {
					rootNodeId = int(x["nodeId"].(float64))
				}
			}
		}

		html, err := chrome.GetOuterHTML(rootNodeId)
		if err != nil {
			log.Printf("chrome.GetOuterHTML: %v", err)
			return
		}
		out.Write([]byte(html))
		done <- true
	})

	// wait until we are done
	<-done
	return out.Bytes(), nil
}
