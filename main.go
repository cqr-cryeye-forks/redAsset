package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

var jsonOutput *json.Encoder
var count = 1
var valid = 0
var tt = time.Now()

func main() {

	parseFile := flag.String("file", "", "JSON file to parse from, gzip allowed.")
	parseDomainFilter := flag.String("domains", "", "File containing 2nd level domains to include.")
	parseDomainBlacklist := flag.String("bdomains", "", "File containing 2nd level domains to exclude.")
	useCATrans := flag.Bool("catransoff", false, "Deactivate querying certificate transparency logs (crt.sh).")

	flag.Parse()

	var allowedDomains []string
	var blacklistDomains []string
	var ips []*net.IPNet
	var wg sync.WaitGroup

	if *parseFile == "" {
		fmt.Println("Usage:")
		flag.PrintDefaults()
		os.Exit(1)
	}

	if *parseDomainFilter != "" {
		var err error
		allowedDomains, ips, err = parseDomainFile(*parseDomainFilter)
		if err != nil {
			log.Fatalf("Error reading domain file: %v", err)
		}

		log.Printf("Limiting to %v 2nd-lvl domains.", len(allowedDomains))
	}

	if *parseDomainBlacklist != "" {
		var err error
		blacklistDomains, ips, err = parseDomainFile(*parseDomainBlacklist)
		if err != nil {
			log.Fatalf("Error reading blacklist domain file: %v", err)
		}

		log.Printf("Limiting to %v blacklisted 2nd-lvl domains.", len(blacklistDomains))
	}

	wg.Add(1)
	go func() {
		log.Println("Parsing FDNS file.")
		parseFDNS(*parseFile, allowedDomains, blacklistDomains, ips)
		wg.Done()
	}()

	if !*useCATrans {
		wg.Add(1)
		go func() {
			log.Println("Querying certificate transparency logs.")
			queryCATransparency(allowedDomains, blacklistDomains)
			wg.Done()
		}()
	}

	wg.Wait()

	log.Println("Finished.")
}

func queryCATransparency(allowed []string, blacklist []string) {
	var bodyDomain []struct {
		Domain string `json:"name_value"`
	}

	for _, domain := range allowed {
		url := fmt.Sprintf("https://crt.sh/?q=%%%v&output=json", domain)
		resp, err := http.Get(url)
		if err != nil {
			log.Printf("Error contacting crt.sh: %s", err)
			continue
		}
		defer resp.Body.Close()

		body, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			continue
		}

		json.Unmarshal([]byte(body), &bodyDomain)

		for _, d := range bodyDomain {
			if isValidResult(DNSEntry{Name: d.Domain}, allowed, blacklist, []*net.IPNet{}) {
				fmt.Println(d.Domain)
				valid++
			}
		}

		log.Printf("CA transparency: Got %v certificates for '%v'", len(bodyDomain), domain)
	}
}

func parseFDNS(fname string, allowed []string, blacklist []string, ips []*net.IPNet) {

	if len(allowed) <= 0 && len(ips) <= 0 {
		log.Fatal("No valid domains (0) and IPs (0) parsed.")
	}

	for dnsentry := range parseDnsHosts(fname) {

		if count%1000000 == 0 && count > 0 {
			log.Printf("FDNS: %vm processed, %v valid (took %v)", count/1000000, valid, time.Since(tt))
			tt = time.Now()
		}

		if isValidResult(dnsentry, allowed, blacklist, ips) {
			fmt.Println(dnsentry.Name)
			valid++
		}

		count++
	}

}

func isValidResult(dnsentry DNSEntry, allowed []string, blacklist []string, ips []*net.IPNet) bool {

	//check if IP is in one of the parsed networks
	if len(ips) > 0 {
		entryIp := net.ParseIP(dnsentry.Value)
		for _, ip := range ips {
			if ip.Contains(entryIp) {
				return true
			}
		}

		// if no allowed domains are passed, stop here
		if len(allowed) <= 0 {
			return false
		}
	}

	// check if allowed domain
	if len(allowed) > 0 {
		if !isAllowed(allowed, dnsentry.Name) {
			return false
		}
	}

	// remove blacklisted domains
	if len(blacklist) > 0 {
		if isAllowed(blacklist, dnsentry.Name) {
			return false
		}
	}

	return true
}

func isAllowed(allowed []string, domain string) bool {

	for _, s := range allowed {
		if strings.HasSuffix(domain, s) {
			return true
		}
	}
	return false
}
