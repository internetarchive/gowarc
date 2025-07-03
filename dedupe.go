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
	date         string
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

		return revisitRecord{
			responseUUID: "",
			size:         recordSize,
			targetURI:    cdxReply[2],
			date:         cdxReply[1],
		}, nil
	}

	return revisitRecord{}, nil
}

func checkDoppelgangerRevisit(DoppelgangerHost string, SHA256Base16 string, targetURI string, recordSize int64, date string, SHA1 string) (revisitRecord, error) {
	var requestData struct {
		URI  string `json:"uri"`
		SHA1 string `json:"sha1"`
		Size int64  `json:"size"`
		Date int64  `json:"date"`
	}

	requestData.URI = targetURI
	requestData.SHA1 = SHA1
	requestData.Size = recordSize
	dateInt, err := strconv.ParseInt(date, 10, 64)
	if err != nil {
		return revisitRecord{}, err
	}
	requestData.Date = dateInt

	jsonData, err := json.Marshal(requestData)
	if err != nil {
		return revisitRecord{}, err
	}

	req, err := http.NewRequest("POST", DoppelgangerHost+"/api/records/"+SHA256Base16, strings.NewReader(string(jsonData)))
	if err != nil {
		return revisitRecord{}, err
	}

	req.Header.Set("Content-Type", "application/json")

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
			ID   string `json:"id" db:"id"`
			URI  string `json:"uri" db:"uri"`
			Date int64  `json:"date" db:"date"`
			SHA1 string `json:"sha1" db:"sha1"`
			Size int64  `json:"size" db:"size"`
		}

		// Parse JSON response
		if err := json.Unmarshal(body, &DoppelgangerJSONResponse); err != nil {
			return revisitRecord{}, err
		}

		return revisitRecord{
			responseUUID: "",
			size:         0,
			targetURI:    DoppelgangerJSONResponse.URI,
			date:         strconv.FormatInt(DoppelgangerJSONResponse.Date, 10),
		}, nil
	}

	return revisitRecord{}, nil
}
