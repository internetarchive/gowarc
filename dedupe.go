package warc

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// TODO: Add stats on how long dedupe HTTP requests take
var DedupeHTTPClient = http.Client{
	Timeout: 10 * time.Second,
	Transport: &http.Transport{
		Dial: (&net.Dialer{
			Timeout: 5 * time.Second,
		}).Dial,
		TLSHandshakeTimeout: 5 * time.Second,
	},
}

type DedupeOptions struct {
	CDXURL             string
	DoppelgangerHost   string
	CDXCookie          string
	SizeThreshold      int
	LocalDedupe        bool
	CDXDedupe          bool
	DoppelgangerDedupe bool
}

type revisitRecord struct {
	responseUUID string
	targetURI    string
	date         time.Time
	size         int
}

func (d *customDialer) checkLocalRevisit(digest string) revisitRecord {
	revisit, exists := d.client.dedupeHashTable.Load(digest)
	if exists {
		return revisit.(revisitRecord)
	}

	return revisitRecord{}
}

func checkCDXRevisit(CDXURL string, digest string, targetURI string, cookie string) (revisitRecord, error) {
	// CDX expects no hash header. For now we need to strip it.
	digest = strings.SplitN(digest, ":", 2)[1]

	req, err := http.NewRequest("GET", CDXURL+"/web/timemap/cdx?url="+url.QueryEscape(targetURI)+"&limit=-1", nil)
	if err != nil {
		return revisitRecord{}, err
	}

	if cookie != "" {
		req.Header.Add("Cookie", cookie)
	}
	resp, err := DedupeHTTPClient.Do(req)
	if err != nil {
		return revisitRecord{}, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return revisitRecord{}, err
	}

	cdxReply := strings.Fields(string(body))

	if len(cdxReply) >= 7 && cdxReply[3] != "warc/revisit" && cdxReply[5] == digest {
		recordSize, _ := strconv.Atoi(cdxReply[6])

		t, err := time.Parse("20060102150405", cdxReply[1])
		if err != nil {
			return revisitRecord{}, err
		}

		return revisitRecord{
			responseUUID: "",
			size:         recordSize,
			targetURI:    cdxReply[2],
			date:         t,
		}, nil
	}

	return revisitRecord{}, nil
}

func checkDoppelgangerRevisit(DoppelgangerHost string, digest string, targetURI string) (revisitRecord, error) {
	// Doppelganger is not expecting a hash header either but this will all be rewritten ... shortly...
	digest = strings.SplitN(digest, ":", 2)[1]

	req, err := http.NewRequest("GET", DoppelgangerHost+"/api/records/"+digest+"?uri="+targetURI, nil)
	if err != nil {
		return revisitRecord{}, err
	}

	// I don't think there's a need to create a new HTTP client, but it does look a little funky.
	resp, err := DedupeHTTPClient.Do(req)
	if err != nil {
		return revisitRecord{}, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return revisitRecord{}, err
	}

	if resp.StatusCode == 200 {
		var DoppelgangerJSONResponse struct {
			ID   string `json:"id"`
			URI  string `json:"uri"`
			Date int64  `json:"date"`
		}
		// Parse JSON response
		if err := json.Unmarshal(body, &DoppelgangerJSONResponse); err != nil {
			return revisitRecord{}, err
		}

		t, err := time.Parse("20060102150405", strconv.FormatInt(DoppelgangerJSONResponse.Date, 10))
		if err != nil {
			return revisitRecord{}, err
		}

		return revisitRecord{
			responseUUID: "",
			size:         0,
			targetURI:    DoppelgangerJSONResponse.URI,
			date:         t,
		}, nil
	}

	return revisitRecord{}, nil
}
