package main

import (
	"bytes"
	"encoding/xml"
	"flag"
	"fmt"
	"html/template"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"

	"gopkg.in/gomail.v2"
	"gopkg.in/yaml.v2"
)

// UserAgent to be used in http requests
const UserAgent = "feeder"

// Feed represents a downloaded news feed
type Feed struct {
	Title   string
	ID      string
	Link    string
	Updated time.Time
	Entries []*FeedEntry

	Failure error
}

// FeedEntry represents a a downloaded news feed entry
type FeedEntry struct {
	Title   string
	Link    string
	ID      string
	Updated time.Time
	Content template.HTML
}

func (e *FeedEntry) Copy() *FeedEntry {
	return &FeedEntry{
		Title:   e.Title,
		Link:    e.Link,
		ID:      e.ID,
		Updated: e.Updated,
		Content: e.Content,
	}
}

type RSSFeed struct { // v2
	XMLName       xml.Name  `xml:"rss"`
	Title         string    `xml:"channel>title"`
	Links         []Link    `xml:"channel>link"`
	LastBuildDate string    `xml:"channel>lastBuildDate"`
	Items         []RSSItem `xml:"channel>item"`
}

type RSSItem struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	Description string `xml:"description"`
	GUID        string `xml:"guid"`
	PubDate     string `xml:"pubDate"`
}

func parseTime(raw string) (t time.Time, err error) {
	t, err = time.Parse(time.RFC1123Z, raw)
	if err == nil {
		return t, nil
	}

	t, err = time.Parse(time.RFC1123, raw)
	if err == nil {
		return t, nil
	}

	t, err = time.Parse(time.RFC3339, raw)
	if err == nil {
		return t, nil
	}

	t, err = time.Parse("2006-01-02T15:04:05-0700", raw)
	if err == nil {
		return t, nil
	}

	return t, fmt.Errorf("failed to parse time string %#v", raw)
}

func (f *RSSFeed) Feed() *Feed {
	if len(f.Links) == 0 {
		log.Fatalf("missing link on feed %#v", f.Title)
	}

	cf := &Feed{
		ID:      f.Links[0].HRef, // 🤨
		Title:   f.Title,
		Link:    f.Links[0].HRef,
		Entries: []*FeedEntry{},
	}

	var err error
	if f.LastBuildDate != "" {
		cf.Updated, err = parseTime(f.LastBuildDate)
		if err != nil {
			log.Fatalf("time parse feed title=%v str=%#v err=%v", f.Title, f.LastBuildDate, err)
		}
	}

	for _, e := range f.Items {
		et, err := parseTime(e.PubDate)
		if err != nil {
			log.Fatalf("time parse str=%#v err=%v", e.PubDate, err)
		}
		ce := &FeedEntry{
			Title:   e.Title,
			Link:    e.Link,
			ID:      e.GUID,
			Updated: et,
			Content: template.HTML(e.Description),
		}
		cf.Entries = append(cf.Entries, ce)
	}
	return cf
}

type AtomFeed struct {
	XMLName xml.Name     `xml:"feed"`
	Title   string       `xml:"title"`
	Link    Link         `xml:"link"`
	Updated xmlTime      `xml:"updated"`
	ID      string       `xml:"id"`
	Entries []*AtomEntry `xml:"entry"`
}

func (f *AtomFeed) Feed() *Feed {
	cf := &Feed{
		ID:      f.ID,
		Title:   f.Title,
		Link:    f.Link.HRef,
		Updated: f.Updated.Time,
		Entries: []*FeedEntry{},
	}
	for _, e := range f.Entries {
		cf.Entries = append(cf.Entries, &FeedEntry{
			Title:   e.Title,
			Link:    e.Link.HRef,
			ID:      e.ID,
			Updated: e.Updated.Time,
			Content: template.HTML(e.Content),
		})
	}

	return cf
}

type xmlTime struct {
	time.Time
}

func (t *xmlTime) UnmarshalXML(d *xml.Decoder, el xml.StartElement) error {
	var v string
	d.CharsetReader = charset.NewReaderLabel
	err := d.DecodeElement(&v, &el)
	if err != nil {
		return err
	}

	t.Time, err = parseTime(v)
	if err != nil {
		return err
	}

	return nil
}

// Link enables us to unmarshal Atom and plain link tags
type Link struct {
	XMLName xml.Name `xml:"link"`
	HRef    string
}

func (l *Link) UnmarshalXML(d *xml.Decoder, el xml.StartElement) error {
	var s string
	d.CharsetReader = charset.NewReaderLabel
	err := d.DecodeElement(&s, &el)
	if err != nil {
		return err
	}

	_, err = url.ParseRequestURI(s)
	if err == nil {
		l.HRef = s
		return nil
	}

	if len(el.Attr) > 0 {
		for _, a := range el.Attr {
			if a.Name.Local == "href" {
				_, err = url.ParseRequestURI(a.Value)
				if err == nil {
					l.HRef = a.Value
					return nil
				}
			}
		}
	}

	return fmt.Errorf("found no href content in link element %#v", el)
}

type AtomEntry struct {
	Title   string  `xml:"title"`
	Link    Link    `xml:"link"`
	Updated xmlTime `xml:"updated"`
	ID      string  `xml:"id"`
	Content string  `xml:"content"`
}

func unmarshal(byt []byte) (*Feed, error) {
	var err error
	var atom AtomFeed
	var rss RSSFeed

	reader := bytes.NewReader(byt)
	decoder := xml.NewDecoder(reader)
	decoder.CharsetReader = charset.NewReaderLabel

	err = decoder.Decode(&atom)
	if err == nil {
		return (&atom).Feed(), nil
	}

	reader = bytes.NewReader(byt)
	decoder = xml.NewDecoder(reader)
	decoder.CharsetReader = charset.NewReaderLabel

	err = decoder.Decode(&rss)
	if err == nil {
		return (&rss).Feed(), nil
	}

	if strings.Contains(err.Error(), "unexpected EOF") {
		log.Printf("ignoring EOF err=%s", err)
		return nil, nil
	}

	return nil, err
}

type FeederFlags struct {
	Config    string
	Subscribe string
}

func readFlags() (*FeederFlags, error) {
	var err error
	flg := &FeederFlags{}

	flags := flag.NewFlagSet("feeder", flag.ExitOnError)
	flags.StringVar(&flg.Config, "config", "", "Path to config file (required)")
	flags.StringVar(&flg.Subscribe, "subscribe", "", "URL to feed to subscribe to")
	flags.Usage = func() {
		fmt.Fprintf(flags.Output(), "Usage of feeder:\n\n")
		flags.PrintDefaults()
		help := `
By default feeder will try to download the configured feeds and send
the latest entries via email. If the subscribe flag is provided, 
instead of downloading feeds, feeder tries to subscribe to the feed 
at the given URL and persists the augmented feeds config.
`
		fmt.Fprintf(flags.Output(), help)
	}

	err = flags.Parse(os.Args[1:])
	if err != nil {
		return nil, err
	}

	if flg.Config == "" {
		return nil, fmt.Errorf("config is required.")
	}

	return flg, nil
}

type Config struct {
	TimestampFile     string      `yaml:"timestamp-file"`
	EmailTemplateFile string      `yaml:"email-template-file"`
	FeedsFile         string      `yaml:"feeds-file"`
	Email             ConfigEmail `yaml:"email"`
}

type ConfigEmail struct {
	From string     `yaml:"from"`
	SMTP ConfigSMTP `yaml:"smtp"`
}

type ConfigSMTP struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
	User string `yaml:"user"`
	Pass string `yaml:"pass"`
}

type ConfigFeed struct {
	Name     string `yaml:"name"`
	URL      string `yaml:"url"`
	Disabled bool   `yaml:"disabled"`
}

func readConfig(fp string) (*Config, error) {
	bt, err := ioutil.ReadFile(fp)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var cf Config
	err = yaml.Unmarshal(bt, &cf)

	if cf.FeedsFile == "" {
		return nil, fmt.Errorf("config is missing feeds-file")
	}

	if cf.TimestampFile == "" {
		return nil, fmt.Errorf("config is missing timestamp-file")
	}

	if cf.Email.From == "" {
		return nil, fmt.Errorf("config is missing email.from")
	}

	if cf.Email.SMTP.Host == "" {
		return nil, fmt.Errorf("config is missing email.smtp.host")
	}

	if cf.Email.SMTP.Port == 0 {
		return nil, fmt.Errorf("config is missing email.smtp.port")
	}

	if cf.Email.SMTP.User == "" {
		return nil, fmt.Errorf("config is missing email.smtp.user")
	}

	if cf.Email.SMTP.Pass == "" {
		return nil, fmt.Errorf("config is missing email.smtp.pass")
	}

	return &cf, err
}

func readFeedsConfig(fp string) ([]*ConfigFeed, error) {
	_, err := os.Stat(fp)
	if os.IsNotExist(err) {
		return []*ConfigFeed{}, nil
	}

	bt, err := ioutil.ReadFile(fp)
	if err != nil {
		return nil, fmt.Errorf("failed to read feeds config file: %w", err)
	}

	var fs []*ConfigFeed
	err = yaml.Unmarshal(bt, &fs)

	return fs, err
}

func failOnErr(cfg *Config, err error) {
	if err != nil {
		if cfg != nil {
			cf := cfg.Email
			m := gomail.NewMessage()
			m.SetHeader("From", cf.From)
			m.SetHeader("To", cf.From)
			m.SetHeader("Subject", "feeder failure")
			m.SetBody("text/plain", err.Error())

			d := gomail.NewDialer(cf.SMTP.Host, cf.SMTP.Port, cf.SMTP.User, cf.SMTP.Pass)
			log.Printf("tried to send failure email err=%v", d.DialAndSend(m))
		}
		log.Fatal(err)
	}
}

func sendEmail(cfg ConfigEmail, body string) error {
	m := gomail.NewMessage()
	m.SetHeader("From", cfg.From)
	m.SetHeader("To", cfg.From)
	m.SetHeader("Subject", fmt.Sprintf("feeder update: %s", time.Now().Format("2006-01-02 15:04")))
	m.SetBody("text/html", body)

	d := gomail.NewDialer(cfg.SMTP.Host, cfg.SMTP.Port, cfg.SMTP.User, cfg.SMTP.Pass)
	return d.DialAndSend(m)
}

func downloadFeeds(cs []*ConfigFeed) ([]*Feed, []*Feed) {
	succs := []*Feed{}
	fails := []*Feed{}
	for _, fc := range cs {
		if fc.Disabled {
			continue
		}

		rf, err := get(fc.URL)
		if err != nil {
			fails = append(fails, &Feed{Title: fc.Name, Link: fc.URL, Failure: err})
			continue
		}

		uf, err := unmarshal(rf)
		if err != nil {
			fails = append(fails, &Feed{Title: fc.Name, Link: fc.URL, Failure: err})
			continue
		}

		succs = append(succs, uf)
	}
	return succs, fails
}

func pickNewData(fs []*Feed, ts map[string]time.Time) []*Feed {
	limitPerFeed := 3
	result := []*Feed{}
	for _, f := range fs {
		nf := &Feed{Title: f.Title, ID: f.ID, Link: f.Link, Updated: f.Updated, Entries: []*FeedEntry{}}
		lt, seen := ts[f.ID]
		for _, e := range f.Entries {
			if !seen || e.Updated.After(lt) {
				nf.Entries = append(nf.Entries, e.Copy())
				if len(nf.Entries) >= limitPerFeed {
					break
				}
			}
		}
		if len(nf.Entries) > 0 {
			result = append(result, nf)
		}
	}
	return result
}

func updateTimestamps(ts map[string]time.Time, nd []*Feed) {
	for _, f := range nd {
		if f.Failure != nil { // TODO don't think this is possible?
			continue
		}
		_, ok := ts[f.ID]
		if !ok {
			ts[f.ID] = f.Entries[0].Updated
		}
		for _, e := range f.Entries {
			if e.Updated.After(ts[f.ID]) {
				ts[f.ID] = e.Updated
			}
		}
	}
}

func readTimestamps(fn string) (map[string]time.Time, error) {
	var err error
	var result map[string]time.Time
	var bt []byte
	var fh *os.File

	fh, err = os.OpenFile(fn, os.O_CREATE, 0677)
	if err != nil {
		return nil, fmt.Errorf("failed to open timestamps file %#v err=%w", fn, err)
	}

	bt, err = ioutil.ReadAll(fh)
	if err != nil {
		return nil, fmt.Errorf("failed to read timestamps file %#v err=%w", fn, err)
	}

	if len(bt) == 0 {
		return map[string]time.Time{}, nil
	}

	err = yaml.Unmarshal(bt, &result)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal timestamps %#v file err=%w", fn, err)
	}

	return result, nil
}

func writeTimestamps(fn string, ts map[string]time.Time) error {
	var err error
	var bt []byte

	bt, err = yaml.Marshal(ts)
	if err != nil {
		return fmt.Errorf("failed to marshal timestamps err=%w", err)
	}

	err = ioutil.WriteFile(fn, bt, 0677)
	if err != nil {
		return fmt.Errorf("failed to write timestamps file err=%w", err)
	}

	return nil
}

// FormatTime prints a time with layout "2006-01-02 15:04 MST"
func FormatTime(t time.Time) string {
	return t.Format("2006-01-02 15:04 MST")
}

var defaultEmailTemplate = `
{{ range .Successes}}
<h1 style="border: 1px solid #acb0bf; border-radius: 3px; background: #f4f4f4; padding: 1em; margin: 1.6em 0;"><a href="{{ .Link }}" style="text-decoration: none; color: RoyalBlue; ">{{ .Title }}</a></h1>
  {{ range .Entries }}
  <h2 style="border: 1px solid #acb0bf; border-radius: 3px; background: #f4f4f4; padding: 1em; margin: 1.6em 0;"><a href="{{ .Link }}" style="text-decoration: none; color: RoyalBlue; ">{{ .Title }}</a><span style="font-size:0.75rem;margin-left:1rem;">{{ FormatTime .Updated }}</span></h2>
  <div>
    {{ .Content }}
  </div>
  {{ end }}
{{ end }}

<br />
<hr />
<br />

{{ range .Failures}}
<h1 style="border: 1px solid #acb0bf; border-radius: 3px; background: #f4f4f4; padding: 1em; margin: 1.6em 0;"><a href="{{ .Link }}" style="text-decoration: none; color: RoyalBlue; ">{{ .Title }}</a></h1>
Failed to process feed: {{ .Failure }}
{{ end }}
`

func readEmailTemplate(fn string) (string, error) {
	if fn == "" {
		return defaultEmailTemplate, nil
	}

	bt, err := ioutil.ReadFile(fn)
	if err != nil {
		return "", fmt.Errorf("failed to read email template file %#v err=%w", fn, err)
	}

	return string(bt), nil
}

type templateData struct {
	Successes []*Feed
	Failures  []*Feed
}

func makeEmailBody(succs []*Feed, fails []*Feed, emailTemplate string) (string, error) {
	fs := template.FuncMap{"FormatTime": FormatTime}
	tmpl, err := template.New("email").Funcs(fs).Parse(emailTemplate)
	if err != nil {
		return "", fmt.Errorf("failed to parse template err=%w", err)
	}

	var buf bytes.Buffer
	err = tmpl.Execute(&buf, &templateData{succs, fails})
	if err != nil {
		return "", fmt.Errorf("failed to execute template err=%w", err)
	}

	return buf.String(), nil
}

func countEntries(fs []*Feed) int {
	c := 0
	for _, f := range fs {
		c += len(f.Entries)
	}
	return c
}

func get(url string) ([]byte, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request for url=%s err=%w", url, err)
	}
	req.Header.Add("User-Agent", UserAgent)

	client := http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to request url=%s err=%w", url, err)
	}

	byt, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read body contents for url=%s err=%w", url, err)
	}
	defer resp.Body.Close()

	return byt, nil
}

func findFeedInfo(byt []byte) (title, link string) {
	doc, err := html.Parse(bytes.NewReader(byt))
	if err != nil {
		log.Fatalf("failed to parse feed as HTML err=%s", err)
	}

	var f func(*html.Node)
	f = func(n *html.Node) {
		if title == "" && n.Type == html.ElementNode && n.Data == "title" && n.FirstChild != nil {
			title = n.FirstChild.Data
			log.Printf("found title: %#v", title)
		}
		if n.Type == html.ElementNode && n.Data == "link" {
			var isAlternate bool
			var href string
			var typ string
			for _, a := range n.Attr {
				switch strings.ToLower(a.Key) {
				case "rel":
					isAlternate = a.Val == "alternate"
				case "type":
					typ = a.Val
				case "href":
					href = a.Val
				case "title":
					if title == "" {
						title = a.Val
					}
				}
			}
			if isAlternate && (typ == "application/rss+xml" || typ == "application/atom+xml") {
				log.Printf("found alternate type=%s href=%s", typ, href)
				link = href
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			f(c)
		}
	}
	f(doc)

	return
}

func subscribe(cfg *Config, fu string) {
	log.Printf("downloading feed %#v\n", fu)
	byt, err := get(fu)
	if err != nil {
		log.Fatalf("failed get feed err=%s", err)
	}

	fc := &ConfigFeed{}

	uf, err := unmarshal(byt)
	if err == nil {
		fc.Name = uf.Title
		fc.URL = fu
	} else {
		fc.Name, fc.URL = findFeedInfo(byt)
		if fc.Name == "" || fc.URL == "" {
			log.Fatalf("failed to find both required title and url")
		}

		u, err := url.Parse(fc.URL)
		if err != nil {
			log.Fatalf("failed to parse feed href=%s as valid url", fc.URL)
		}

		if !u.IsAbs() {
			base, err := url.Parse(fu)
			if err != nil {
				log.Fatalf("failed to parse feed url err=%s", err)
			}
			fc.URL = base.ResolveReference(u).String()
		}
	}

	ef, err := readFeedsConfig(cfg.FeedsFile)
	if err != nil {
		log.Fatalf("failed to read feeds config err=%s", err)
	}
	log.Printf("read feeds config: %v feeds.", len(ef))

	for _, f := range ef {
		if strings.ToLower(f.URL) == strings.ToLower(fc.URL) {
			log.Printf("feed URL already present in existing feeds, no need to subscribe")
			os.Exit(0)
		}
	}
	nf := append(ef, fc)

	var bt []byte
	bt, err = yaml.Marshal(nf)
	if err != nil {
		log.Fatalf("failed to marshal feeds err=%s", err)
	}

	err = ioutil.WriteFile(cfg.FeedsFile, bt, 0677)
	if err != nil {
		log.Fatalf("failed to write timestamps file err=%s", err)
	}

	log.Printf("successfully subscribed to feed title=%#v url=%#v", fc.Name, fc.URL)
}

func feed(cfg *Config) {
	var err error
	var fs []*ConfigFeed
	var ts map[string]time.Time
	var succs, fails, nd []*Feed
	var et string

	ts, err = readTimestamps(cfg.TimestampFile)
	failOnErr(cfg, err)
	log.Printf("read timestamps from %#v\n", cfg.TimestampFile)

	et, err = readEmailTemplate(cfg.EmailTemplateFile)
	failOnErr(cfg, err)

	fs, err = readFeedsConfig(cfg.FeedsFile)
	failOnErr(cfg, err)
	log.Printf("read feeds config: %v feeds.", len(fs))

	succs, fails = downloadFeeds(fs)
	log.Printf("downloaded %v feeds successfully, %v failures\n", len(succs), len(fails))

	nd = pickNewData(succs, ts)
	if len(nd) == 0 && len(fails) == 0 {
		log.Printf("found no new entries")
		return
	}
	log.Printf("found %v new entries\n", countEntries(nd))

	emailBody, err := makeEmailBody(nd, fails, et)
	failOnErr(cfg, err)

	err = sendEmail(cfg.Email, emailBody)
	failOnErr(cfg, err)
	log.Printf("sent email\n")

	updateTimestamps(ts, nd)
	err = writeTimestamps(cfg.TimestampFile, ts)
	failOnErr(cfg, err)
	log.Printf("wrote updated timestamps to %#v\n", cfg.TimestampFile)
}

func main() {
	var err error
	var flg *FeederFlags
	var cfg *Config

	flg, err = readFlags()
	failOnErr(cfg, err)

	cfg, err = readConfig(flg.Config)
	failOnErr(cfg, err)
	log.Printf("read config\n")

	if flg.Subscribe != "" {
		subscribe(cfg, flg.Subscribe)
		return
	}

	feed(cfg)
}
