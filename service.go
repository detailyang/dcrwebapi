package main

import (
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	// SVGTemplate the svg template
	SVGTemplate = `<svg xmlns="http://www.w3.org/2000/svg" width="128" height="20"><linearGradient id="b" x2="0" y2="100%"><stop offset="0" stop-color="#bbb" stop-opacity=".1"/><stop offset="1" stop-opacity=".1"/></linearGradient><mask id="a"><rect width="128" height="20" rx="3" fill="#fff"/></mask><g mask="url(#a)"><path fill="#555" d="M0 0h69v20H0z"/><path fill="#4c1" d="M69 0h59v20H69z"/><path fill="url(#b)" d="M0 0h128v20H0z"/></g><g fill="#fff" text-anchor="middle" font-family="DejaVu Sans,Verdana,Geneva,sans-serif" font-size="11"><text x="34.5" y="15" fill="#010101" fill-opacity=".3">downloads</text><text x="34.5" y="14">downloads</text><text x="97.5" y="15" fill="#010101" fill-opacity=".3">__COUNT__ total</text><text x="97.5" y="14">__COUNT__ total</text></g></svg>`
	// StakepoolAPIInitialVersion the stakepool api initial version
	StakepoolAPIInitialVersion = 1
	// StakepoolAPICurrentVersion the stakepool api current version
	StakepoolAPICurrentVersion = 2
)

// Stakepool represents a decred stakepool solely for voting delegation.
type Stakepool struct {
	// the api enabled flag
	APIEnabled bool `json:"APIEnabled"`
	// a collection of api versions supported
	APIVersionsSupported []interface{} `json:"APIVersionsSupported"`
	// the active network
	Network string `json:"Network"`
	// the stakepool url
	URL string `json:"URL"`
	// the last updated time
	LasUpdated int64 `json:"LastUpdated"`
	// tmmature tickets
	Immature int `json:"Immature"`
	// live tickets
	Live int `json:"Live"`
	// voted tickets
	Voted int `json:"Voted"`
	// missed votes
	Missed int `json:"Missed"`
	// the stakepool fees
	PoolFees float64 `json:"PoolFees"`
	// ticket proportion live
	ProportionLive float64 `json:"ProportionLive"`
	// the user count
	UserCount int `json:"UserCount"`
	// the active user count
	UserCountActive int `json:"UserCountActive"`
}

// CacheEntry represents a cache entry with a specified expiry
type CacheEntry struct {
	// the cached item
	Item interface{}
	// the cache expiry
	Expiry *time.Time
}

// Service represents the derweb service
type Service struct {
	// the http client
	HTTPClient *http.Client
	// the http router
	Router *http.ServeMux
	// the service cache
	Cache sync.Map
}

// NewService creates a dcrwebapi service
func NewService() *Service {
	service := &Service{}
	service.HTTPClient = &http.Client{
		Transport: &http.Transport{
			MaxIdleConnsPerHost: 2,
			TLSClientConfig:     &tls.Config{InsecureSkipVerify: false},
		},
		Timeout: time.Second * 8,
	}
	service.Router = http.NewServeMux()
	service.Cache = sync.Map{}

	// setup route
	service.Router.HandleFunc("/", service.HandleRoutes)
	return service
}

// filterDownloadCount filters the download dataset returning a map of
// asset filenames and their download counts
func filterDownloadCount(count *int64, dataset *[]interface{}) {
	for _, entry := range *dataset {
		entry := entry.(map[string]interface{})
		_, hasAssets := entry["assets"].([]interface{})
		if hasAssets {
			assets := entry["assets"].([]interface{})
			for _, asset := range assets {
				asset := asset.(map[string]interface{})
				_, hasName := asset["name"].(string)
				_, hasDownloadCount := asset["download_count"].(float64)
				if hasName && hasDownloadCount {
					*count += int64(asset["download_count"].(float64))
				}
			}
		}
	}
}

// downloadCount calculates the cummulative download count for DCR binaries and releases
func downloadCount(service *Service) (*[]string, error) {
	now := time.Now()
	entry, hasDc := service.Cache.Load("dc")
	if hasDc {
		// return cached response if not invalidated
		entry := entry.(CacheEntry)
		if now.Before(*entry.Expiry) {
			resp := entry.Item.([]string)
			return &resp, nil
		}
	}

	var count int64
	// fetch all binaries
	binReq, err := http.NewRequest("GET", "https://api.github.com/repos/decred/decred-binaries/releases", nil)
	if err != nil {
		return nil, err
	}

	binReq.Header.Set("User-Agent", "decred/dcrweb bot")
	binResp, err := service.HTTPClient.Do(binReq)
	if err != nil {
		return nil, err
	}

	defer binResp.Body.Close()
	binBody, err := ioutil.ReadAll(binResp.Body)
	if err != nil {
		return nil, err
	}

	binaries := &[]interface{}{}
	err = json.Unmarshal(binBody, binaries)
	if err != nil {
		return nil, err
	}

	filterDownloadCount(&count, binaries)

	// fetch all releases
	relReq, err := http.NewRequest("GET", "https://api.github.com/repos/decred/decred-release/releases", nil)
	if err != nil {
		return nil, err
	}

	relReq.Header.Set("User-Agent", "decred/dcrweb bot")
	relResp, err := service.HTTPClient.Do(relReq)
	if err != nil {
		return nil, err
	}

	defer relResp.Body.Close()
	relBody, err := ioutil.ReadAll(relResp.Body)
	if err != nil {
		return nil, err
	}

	releases := &[]interface{}{}
	err = json.Unmarshal(relBody, releases)
	if err != nil {
		return nil, err
	}

	filterDownloadCount(&count, releases)

	countStr := fmt.Sprintf("%dk", count/1000)
	resp := &[]string{"DownloadsCount", countStr}
	// cache response
	cacheEntry := CacheEntry{
		Item:   *resp,
		Expiry: getFutureTime(&now, 0, 4, 0, 0),
	}
	service.Cache.Store("dc", cacheEntry)

	return resp, nil
}

// downloadsImageCache generates a downloads count image svg
func downloadsImageCache(service *Service) (string, error) {
	now := time.Now()
	entry, hasDic := service.Cache.Load("dic")
	if hasDic {
		// return cached response if not invalidated
		entry := entry.(CacheEntry)
		if now.Before(*entry.Expiry) {
			resp := entry.Item.(string)
			return resp, nil
		}
	}

	// get the assets download count
	var dlCount string
	entry, hasDc := service.Cache.Load("dc")
	if hasDc {
		entry := entry.(CacheEntry)
		if now.Before(*entry.Expiry) {
			resp := entry.Item.([]string)
			dlCount = resp[1]
		}
	}

	if len(dlCount) == 0 {
		resp, err := downloadCount(service)
		if err != nil {
			return "", err
		}

		dlCount = (*resp)[1]
	}

	updatedSVG := strings.Replace(SVGTemplate, "__COUNT__", dlCount, -1)
	// cache response
	cacheEntry := CacheEntry{
		Item:   updatedSVG,
		Expiry: getFutureTime(&now, 0, 4, 0, 0),
	}
	service.Cache.Store("dic", cacheEntry)
	return updatedSVG, nil
}

// insightStatus fetches blockchain explorer related statistics
// (mainnet.decred.org)
func insightStatus(service *Service) (*map[string]interface{}, error) {
	now := time.Now()
	entry, hasGIS := service.Cache.Load("gis")
	if hasGIS {
		// return cached response if not invalidated
		entry := entry.(CacheEntry)
		if now.Before(*entry.Expiry) {
			resp := entry.Item.(map[string]interface{})
			return &resp, nil
		}
	}

	statusReq, err := http.NewRequest("GET",
		"https://mainnet.decred.org/api/status", nil)
	if err != nil {
		return nil, err
	}

	statusReq.Header.Set("User-Agent", "decred/dcrweb bot")
	statusResp, err := service.HTTPClient.Do(statusReq)
	if err != nil {
		return nil, err
	}

	defer statusResp.Body.Close()
	statusBody, err := ioutil.ReadAll(statusResp.Body)
	if err != nil {
		return nil, err
	}

	status := &map[string]interface{}{}
	err = json.Unmarshal(statusBody, status)
	if err != nil {
		return nil, err
	}

	// cache response
	cacheEntry := CacheEntry{
		Item:   *status,
		Expiry: getFutureTime(&now, 0, 0, 1, 0),
	}
	service.Cache.Store("gis", cacheEntry)
	return status, nil
}

// coinSupply returns the DCR coin supply on mainnet
func coinSupply(service *Service) (*map[string]interface{}, error) {
	now := time.Now()
	entry, hasGSC := service.Cache.Load("gsc")
	if hasGSC {
		// return cached response if not invalidated
		entry := entry.(CacheEntry)
		if now.Before(*entry.Expiry) {
			resp := entry.Item.(map[string]interface{})
			return &resp, nil
		}
	}

	supplyReq, err := http.NewRequest("GET", "https://mainnet.decred.org/api/status?q=getCoinSupply", nil)
	if err != nil {
		return nil, err
	}

	supplyReq.Header.Set("User-Agent", "decred/dcrweb bot")
	supplyResp, err := service.HTTPClient.Do(supplyReq)
	if err != nil {
		return nil, err
	}

	defer supplyResp.Body.Close()
	supplyBody, err := ioutil.ReadAll(supplyResp.Body)
	if err != nil {
		return nil, err
	}

	supply := &map[string]interface{}{}
	err = json.Unmarshal(supplyBody, supply)
	if err != nil {
		return nil, err
	}

	currentCoinSupply := round((*supply)["coinsupply"].(float64), 1)
	airdrop := 840000.0
	premine := 840000.0
	coinSupplyAvailable := round(currentCoinSupply/100000000, 0)
	coinSupplyAfterGenesisBlock := coinSupplyAvailable - airdrop - premine
	totalCoinSupply := 21000000.0
	resp := map[string]interface{}{
		"PercentMined":       round((coinSupplyAvailable/totalCoinSupply)*100, 1),
		"CoinSupplyMined":    coinSupplyAvailable,
		"CoinSupplyMinedRaw": currentCoinSupply,
		"CoinSupplyTotal":    21000000,
		"Airdrop":            round(airdrop/coinSupplyAvailable*100, 1),
		"Premine":            round(premine/coinSupplyAvailable*100, 1),
		"Pos":                round((coinSupplyAfterGenesisBlock*.3)/coinSupplyAvailable*100, 1),
		"Pow":                round((coinSupplyAfterGenesisBlock*.6)/coinSupplyAvailable*100, 1),
		"Subsidy":            round((coinSupplyAfterGenesisBlock*.1)/coinSupplyAvailable*100, 1),
	}

	// cache response
	cacheEntry := CacheEntry{
		Item:   resp,
		Expiry: getFutureTime(&now, 0, 0, 1, 0),
	}
	service.Cache.Store("gsc", cacheEntry)

	return &resp, nil
}

// stakepoolStats fetches statistics for a stakepool
func stakepoolStats(service *Service, pool *Stakepool, apiVersion int) error {
	poolURL := fmt.Sprintf("%s/api/v%d/stats", pool.URL, apiVersion)
	poolReq, err := http.NewRequest("GET", poolURL, nil)
	if err != nil {
		return err
	}

	poolReq.Header.Set("User-Agent", "decred/dcrweb bot")
	poolResp, err := service.HTTPClient.Do(poolReq)
	if err != nil {
		return err
	}

	defer poolResp.Body.Close()
	poolRespBody, err := ioutil.ReadAll(poolResp.Body)
	if err != nil {
		return err
	}

	if poolResp.StatusCode != http.StatusOK {
		return err
	}

	poolData := &map[string]interface{}{}
	err = json.Unmarshal(poolRespBody, poolData)
	if err != nil {
		return err
	}

	status := (*poolData)["status"].(string)
	if status == "success" {
		data := (*poolData)["data"].(map[string]interface{})
		_, hasImmature := data["Immature"]
		_, hasLive := data["Live"]
		_, hasVoted := data["Voted"]
		_, hasMissed := data["Missed"]
		_, hasPoolFees := data["PoolFees"]
		_, hasProportionLive := data["ProportionLive"]
		_, hasUserCount := data["UserCount"]
		_, hasUserCountActive := data["UserCountActive"]
		_, hasAPIVersionsSupported := data["APIVersionsSupported"]
		hasRequiredFields := hasImmature && hasLive && hasVoted &&
			hasMissed && hasPoolFees && hasProportionLive &&
			hasUserCount && hasUserCountActive && hasAPIVersionsSupported
		if hasRequiredFields {
			pool.Immature = int(data["Immature"].(float64))
			pool.Live = int(data["Live"].(float64))
			pool.Voted = int(data["Voted"].(float64))
			pool.Missed = int(data["Missed"].(float64))
			pool.PoolFees = data["PoolFees"].(float64)
			pool.ProportionLive = data["ProportionLive"].(float64)
			pool.UserCount = int(data["UserCount"].(float64))
			pool.UserCountActive = int(data["UserCountActive"].(float64))
			pool.APIVersionsSupported = data["APIVersionsSupported"].([]interface{})
			pool.APIEnabled = true
			pool.LasUpdated = time.Now().Unix()
			return nil
		}
	}

	return errors.New("expected success status")
}

// stakepoolData fetches statistics for all listed DCR stakepools
func stakepoolData(service *Service) (*map[string]Stakepool, error) {
	stakepools := &map[string]Stakepool{}
	(*stakepools)["Bravo"] = Stakepool{
		APIVersionsSupported: []interface{}{},
		Network:              "mainnet",
		URL:                  "https://dcr.stakepool.net",
	}
	(*stakepools)["Delta"] = Stakepool{
		APIVersionsSupported: []interface{}{},
		Network:              "mainnet",
		URL:                  "https://dcr.stakeminer.com",
	}
	(*stakepools)["Echo"] = Stakepool{
		APIVersionsSupported: []interface{}{},
		Network:              "mainnet",
		URL:                  "https://pool.d3c.red",
	}
	// Stakepool APi is unreachable for Foxtrot
	// (*stakepools)["Foxtrot"] = Stakepool{
	// 	APIVersionsSupported: []interface{}{},
	// 	Network:              "mainnet",
	// 	URL:                  "https://dcrstakes.com",
	// }
	(*stakepools)["Golf"] = Stakepool{
		APIVersionsSupported: []interface{}{},
		Network:              "mainnet",
		URL:                  "https://stakepool.dcrstats.com",
	}
	(*stakepools)["Hotel"] = Stakepool{
		APIVersionsSupported: []interface{}{},
		Network:              "mainnet",
		URL:                  "https://stake.decredbrasil.com",
	}
	(*stakepools)["India"] = Stakepool{
		APIVersionsSupported: []interface{}{},
		Network:              "mainnet",
		URL:                  "https://stakepool.eu",
	}
	(*stakepools)["Juliett"] = Stakepool{
		APIVersionsSupported: []interface{}{},
		Network:              "mainnet",
		URL:                  "https://dcr.ubiqsmart.com",
	}
	(*stakepools)["Kilo"] = Stakepool{
		APIVersionsSupported: []interface{}{},
		Network:              "testnet",
		URL:                  "https://teststakepool.decred.org",
	}
	(*stakepools)["Lima"] = Stakepool{
		APIVersionsSupported: []interface{}{},
		Network:              "mainnet",
		URL:                  "https://ultrapool.eu",
	}
	(*stakepools)["Mike"] = Stakepool{
		APIVersionsSupported: []interface{}{},
		Network:              "mainnet",
		URL:                  "https://dcr.farm",
	}
	(*stakepools)["November"] = Stakepool{
		APIVersionsSupported: []interface{}{},
		Network:              "mainnet",
		URL:                  "https://decred.raqamiya.net",
	}

	resp := &map[string]Stakepool{}
	entry, hasGSD := service.Cache.Load("gsd")
	now := time.Now()
	if hasGSD {
		// return cached response if not invalidated
		entry := entry.(CacheEntry)
		if now.Before(*entry.Expiry) {
			resp := entry.Item.(map[string]Stakepool)
			return &resp, nil
		}
	}

	waitGroup := sync.WaitGroup{}
	mtx := sync.Mutex{}
	waitGroup.Add(len(*stakepools))
	for key := range *stakepools {
		// fetch the stakepool stats, trying the current version first
		// and the initial version if an error occurs
		go func(key string, stakepools *map[string]Stakepool, service *Service) {
			defer waitGroup.Done()
			stakepool := (*stakepools)[key]
			err := stakepoolStats(service, &stakepool, StakepoolAPICurrentVersion)
			if err != nil {
				log.Println(err)
				err := stakepoolStats(service, &stakepool, StakepoolAPIInitialVersion)
				if err != nil {
					log.Println(err)
					return
				}
			}

			mtx.Lock()
			defer mtx.Unlock()
			(*resp)[key] = stakepool
		}(key, stakepools, service)
	}

	waitGroup.Wait()
	now = time.Now()
	// cache updated pool
	cacheEntry := CacheEntry{
		Item:   *resp,
		Expiry: getFutureTime(&now, 0, 0, 20, 0),
	}
	service.Cache.Store("gsd", cacheEntry)
	return resp, nil
}

// GetCoinSupply is the handler func for the `/gsc` route.
// It returns statistics on the DCR blockchain and the available coin supply
func (service *Service) GetCoinSupply(writer http.ResponseWriter, request *http.Request) {
	resp, err := coinSupply(service)
	if err != nil {
		writeJSONErrorResponse(&writer, http.StatusInternalServerError, err)
		return
	}

	respJSON, err := json.Marshal(resp)
	if err != nil {
		writeJSONErrorResponse(&writer, http.StatusInternalServerError, err)
		return
	}

	writeJSONResponse(&writer, http.StatusOK, &respJSON)
}

// HandleRoutes is the handler func for all endpoints exposed by the service
func (service *Service) HandleRoutes(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Access-Control-Allow-Origin", "*")
	err := request.ParseForm()
	if err != nil {
		writeJSONErrorResponse(&writer, http.StatusInternalServerError, err)
		return
	}

	route := request.FormValue("c")
	switch route {
	case "dc":
		resp, err := downloadCount(service)
		if err != nil {
			writeJSONErrorResponse(&writer, http.StatusInternalServerError, err)
			return
		}

		respJSON, err := json.Marshal(resp)
		if err != nil {
			writeJSONErrorResponse(&writer, http.StatusInternalServerError, err)
			return
		}

		writeJSONResponse(&writer, http.StatusOK, &respJSON)
		return
	case "dic":
		resp, err := downloadsImageCache(service)
		if err != nil {
			writeJSONErrorResponse(&writer, http.StatusInternalServerError, err)
			return
		}

		writeSVGResponse(&writer, http.StatusOK, &resp)
		return
	case "gis":
		resp, err := insightStatus(service)
		if err != nil {
			writeJSONErrorResponse(&writer, http.StatusInternalServerError, err)
			return
		}

		respJSON, err := json.Marshal(resp)
		if err != nil {
			writeJSONErrorResponse(&writer, http.StatusInternalServerError, err)
			return
		}

		writeJSONResponse(&writer, http.StatusOK, &respJSON)
		return
	case "gcs":
		resp, err := coinSupply(service)
		if err != nil {
			writeJSONErrorResponse(&writer, http.StatusInternalServerError, err)
			return
		}

		respJSON, err := json.Marshal(resp)
		if err != nil {
			writeJSONErrorResponse(&writer, http.StatusInternalServerError, err)
			return
		}

		writeJSONResponse(&writer, http.StatusOK, &respJSON)
		return
	case "gsd":
		resp, err := stakepoolData(service)
		if err != nil {
			writeJSONErrorResponse(&writer, http.StatusInternalServerError, err)
			return
		}

		respJSON, err := json.Marshal(resp)
		if err != nil {
			writeJSONErrorResponse(&writer, http.StatusInternalServerError, err)
			return
		}

		writeJSONResponse(&writer, http.StatusOK, &respJSON)
		return
	case "cc":
		addr, _, err := net.SplitHostPort(request.RemoteAddr)
		if err != nil {
			writeJSONErrorResponse(&writer, http.StatusInternalServerError, err)
		}

		if addr == "::1" || addr == "127.0.0.1" {
			service.Cache = sync.Map{}
			respJSON := []byte(`{"response": "cache cleared"}`)
			writeJSONResponse(&writer, http.StatusOK, &respJSON)
			return
		}

		respJSON := []byte(`{"response": "unauthorized"}`)
		writeJSONResponse(&writer, http.StatusBadRequest, &respJSON)
		return
	default:
		writer.WriteHeader(http.StatusNotFound)
		return
	}
}
