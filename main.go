package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/google/go-github/github"
	"golang.org/x/oauth2"
	"io/ioutil"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	week       = 7 * 24 * time.Hour
	timeFormat = "1-2-2006"
)

var (
	tokenFile = flag.String("token_file", "", "Path to the token file")
	user      = flag.String("user", "Harwayne", "GitHub user name")
	start     = flag.String("start", lastCompletedWeekMonday().Format(timeFormat), "Start date in '%m-%d-%y' format")
	duration  = flag.Duration("duration", week, "Duration of time from the start")
)

func lastCompletedWeekMonday() time.Time {
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	switch today.Weekday() {
	case time.Monday:
		return today.AddDate(0, 0, -7)
	case time.Tuesday:
		return today.AddDate(0, 0, -8)
	case time.Wednesday:
		return today.AddDate(0, 0, -9)
	case time.Thursday:
		return today.AddDate(0, 0, -10)
	case time.Friday:
		return today.AddDate(0, 0, -11)
	case time.Saturday:
		return today.AddDate(0, 0, -12)
	case time.Sunday:
		return today.AddDate(0, 0, -13)
	}
	log.Fatal("Couldn't calculate last monday")
	return time.Time{}
}

func main() {
	flag.Parse()

	startTime, err := time.Parse(timeFormat, *start)
	if err != nil {
		log.Fatalf("Unable to parse start time '%s': %v", *start, err)
	}

	log.Printf("Searching for events between %v and %v", startTime.Format(timeFormat), startTime.Add(*duration).Format(timeFormat))
	client := github.NewClient(oauthClient())
	events := listEvents(client)
	fe := filterEventsForTime(events, startTime)
	ge := organizeEvents(fe)
	oe := ge.makeEventSets(client)
	md := oe.markdown()
	fmt.Println(md)
}

func oauthClient() *http.Client {
	oauthToken := readOauthToken()
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: oauthToken})
	return oauth2.NewClient(context.Background(), ts)
}

func readOauthToken() string {
	b, err := ioutil.ReadFile(*tokenFile)
	if err != nil {
		log.Fatalf("Unable to read tokenFile, '%s': %v", *tokenFile, err)
	}
	s := string(b)
	return strings.TrimSuffix(s, "\n")
}

func listEvents(client *github.Client) []*github.Event {
	events := make([]*github.Event, 0)
	page := 1
	for {
		e, r, err := client.Activity.ListEventsPerformedByUser(context.TODO(), *user, true, &github.ListOptions{
			Page: page,
		})
		if err != nil {
			log.Fatalf("Unable to list events for page %v: %v", page, err)
		}
		events = append(events, e...)
		page = r.NextPage
		if page == 0 {
			return events
		}
	}
}

func filterEventsForTime(unfiltered []*github.Event, startTime time.Time) []*github.Event {
	endTime := startTime.Add(*duration)

	events := make([]*github.Event, 0)
	for _, e := range unfiltered {
		if e.CreatedAt.After(startTime) && e.CreatedAt.Before(endTime) {
			events = append(events, e)
		}
	}
	return events
}

type url string
type urlSet map[url]struct{}

type eventSets struct {
	names       map[url]string
	merged      urlSet
	abandoned   urlSet
	underReview urlSet
	inProgress  urlSet
	reviewed    urlSet
	issues      urlSet
}

type issuesAndPRs struct {
	names  map[url]string
	issues map[url]*github.Issue
	prs    map[url]prInfo
}

type prInfo struct {
	owner  string
	repo   string
	number int
}

func organizeEvents(events []*github.Event) *issuesAndPRs {
	parsed := make([]interface{}, 0, len(events))
	for _, event := range events {
		p, err := event.ParsePayload()
		if err != nil {
			log.Fatalf("Unable to parse event: %v, %v", err, event)
		}
		parsed = append(parsed, p)
	}
	ge := &issuesAndPRs{
		names:  make(map[url]string),
		issues: make(map[url]*github.Issue),
		prs:    make(map[url]prInfo),
	}
	for _, event := range parsed {
		switch e := event.(type) {
		case *github.IssueCommentEvent:
			ge.addIssue(e.Issue)
		case *github.IssuesEvent:
			ge.addIssue(e.Issue)
		case *github.PullRequestEvent:
			ge.addPR(e.PullRequest)
		case *github.PullRequestReviewCommentEvent:
			ge.addPR(e.PullRequest)
		// Everything below this line is ignored for now.
		case *github.CommitCommentEvent:
			log.Printf("Hit a commitCommentEvent")
		case *github.CreateEvent:
		case *github.PushEvent:
		case *github.DeleteEvent:
		default:
			log.Printf("Hit some other event type: %T", event)
		}
	}
	return ge
}

func (e *eventSets) markdown() string {
	md := make([]string, 0)
	md = append(md, "* GitHub")
	md = append(md, printSection(e.names, e.merged, "Merged")...)
	md = append(md, printSection(e.names, e.abandoned, "Abandoned")...)
	md = append(md, printSection(e.names, e.underReview, "Under Review")...)
	md = append(md, printSection(e.names, e.inProgress, "In Progress")...)
	md = append(md, printSection(e.names, e.reviewed, "Reviewed")...)
	md = append(md, printSection(e.names, e.issues, "Issues")...)

	markdown := strings.Join(md, "\n")
	markdown = strings.Replace(markdown, "\t", "    ", -1)
	return markdown
}

func printSection(names map[url]string, section map[url]struct{}, title string) []string {
	if len(section) > 0 {
		md := make([]string, 0, len(section)+1)
		md = append(md, fmt.Sprintf("\t* %s", title))
		for url := range section {
			if name, ok := names[url]; ok {
				md = append(md, fmt.Sprintf("\t\t* %s", name))
			} else {
				log.Printf("Did not have a name for: %q", url)
			}
		}
		return md
	}
	return make([]string, 0)
}

type nameable interface {
	GetTitle() string
	GetHTMLURL() string
}

func (e *issuesAndPRs) addName(n nameable) url {
	url := url(n.GetHTMLURL())
	// Since we iterate in reverse chronological order, the first entry, should be the most
	// up-to-date.
	if _, ok := e.names[url]; !ok {
		e.overrideName(url, n)
	}
	return url
}

func (e *issuesAndPRs) overrideName(url url, n nameable) {
	e.names[url] = fmt.Sprintf("[%s](%s)", n.GetTitle(), n.GetHTMLURL())
}

func (e *issuesAndPRs) addIssue(i *github.Issue) {
	url := e.addName(i)
	if i.IsPullRequest() {
		e.prs[url] = crackPRInfo(i.GetHTMLURL())
	} else if _, ok := e.issues[url]; !ok {
		e.issues[url] = i
	}
}

func (e *issuesAndPRs) addPR(pr *github.PullRequest) {
	url := e.addName(pr)
	if _, ok := e.prs[url]; !ok {
		e.prs[url] = crackPRInfo(pr.GetHTMLURL())
	}
}

func (e *eventSets) addName(n nameable) {
	url := url(n.GetHTMLURL())
	// Since we iterate in reverse chronological order, the first entry, should be the most
	// up-to-date.
	if _, ok := e.names[url]; !ok {
		e.names[url] = fmt.Sprintf("[%s](%s)", n.GetTitle(), n.GetHTMLURL())
	}
}

func (e *issuesAndPRs) makeEventSets(client *github.Client) *eventSets {
	eventSets := &eventSets{
		names:       e.names,
		merged:      urlSet{},
		abandoned:   urlSet{},
		underReview: urlSet{},
		inProgress:  urlSet{},
		reviewed:    urlSet{},
		issues:      urlSet{},
	}

	// Make sure we have the newest PRs.
	prs := e.getPRs(client)
	for url, pr := range prs {
		if pr.GetUser().GetLogin() != *user {
			eventSets.reviewed[url] = struct{}{}
			continue
		}
		switch pr.GetState() {
		case "open":
			if isWorkInProgress(pr.Labels) {
				eventSets.inProgress[url] = struct{}{}
			} else {
				eventSets.underReview[url] = struct{}{}
			}
		case "closed":
			if pr.GetMerged() {
				eventSets.merged[url] = struct{}{}
			} else {
				eventSets.abandoned[url] = struct{}{}
			}
		default:
			log.Printf("PR is in an unknown state: %+v", pr)
		}
	}

	for url := range e.issues {
		eventSets.issues[url] = struct{}{}
	}

	eventSets.names = e.names
	return eventSets
}

func (e *issuesAndPRs) getPRs(client *github.Client) map[url]*github.PullRequest {
	newPRs := make(map[url]*github.PullRequest)
	for url, pr := range e.prs {
		newPR, _, err := client.PullRequests.Get(context.TODO(), pr.owner, pr.repo, pr.number)
		if err != nil {
			log.Fatalf("Unable to get PR: %q, %v", url, err)
		}
		newPRs[url] = newPR
		e.overrideName(url, newPR)
	}
	return newPRs
}

func isWorkInProgress(labels []*github.Label) bool {
	for _, l := range labels {
		if l.GetName() == "do-not-merge/work-in-progress" {
			return true
		}
	}
	return false
}

func crackPRInfo(url string) prInfo {
	prefix := "https://github.com/"
	if !strings.HasPrefix(url, prefix) {
		log.Fatalf("Bad prefix: %q", url)
	}
	url = strings.TrimPrefix(url, prefix)
	splits := strings.Split(url, "/")
	if len(splits) < 4 {
		log.Fatalf("Incorrect number of splits: %q", url)
	}
	n, err := strconv.Atoi(splits[3])
	if err != nil {
		log.Fatalf("Unable to parse the fourth split: %q", url)
	}
	return prInfo{
		owner:  splits[0],
		repo:   splits[1],
		number: n,
	}
}
