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
	oe := organizeEvents(fe)
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

type eventSets struct {
	names map[url]string
	merged      map[url]struct{}
	abandoned   map[url]struct{}
	underReview map[url]struct{}
	inProgress  map[url]struct{}
	reviewed    map[url]struct{}
	issues      map[url]struct{}
}

func organizeEvents(events []*github.Event) *eventSets {
	parsed := make([]interface{}, 0, len(events))
	for _, event := range events {
		p, err := event.ParsePayload()
		if err != nil {
			log.Fatalf("Unable to parse event: %v, %v", err, event)
		}
		parsed = append(parsed, p)
	}
	eventSets := &eventSets{
		names: make(map[url]string),
		merged:      make(map[url]struct{}),
		abandoned:   make(map[url]struct{}),
		underReview: make(map[url]struct{}),
		inProgress:  make(map[url]struct{}),
		reviewed:    make(map[url]struct{}),
		issues:      make(map[url]struct{}),
	}
	for _, event := range parsed {
		switch e := event.(type) {

		case *github.IssueCommentEvent:
			eventSets.addName(e.Issue)
			if e.Issue.IsPullRequest() {
				if e.Issue.User.GetLogin() == *user {
					eventSets.underReview[getURL(e.Issue)] = struct{}{}
				} else {
					eventSets.reviewed[getURL(e.Issue)] = struct{}{}
				}
			} else {
				eventSets.issues[getURL(e.Issue)] = struct{}{}
			}
		case *github.IssuesEvent:
			switch e.GetAction() {
			case "opened":
			case "edited":
			case "deleted":
			case "closed":
			case "assigned":
			}
			eventSets.addName(e.Issue)
			if e.Issue.IsPullRequest() {
				eventSets.reviewed[getURL(e.Issue)] = struct{}{}
			} else {
				eventSets.issues[getURL(e.Issue)] = struct{}{}
				log.Printf("Added issueevent %s", getURL(e.Issue))
			}
		case *github.PullRequestEvent:
			eventSets.addName(e.PullRequest)
			switch e.GetAction() {
			case "opened":
				if strings.Contains(e.PullRequest.GetTitle(), "WIP") {
					eventSets.inProgress[getURL(e.PullRequest)] = struct{}{}
				} else {
					eventSets.underReview[getURL(e.PullRequest)] = struct{}{}
				}
			case "edited":
				eventSets.inProgress[getURL(e.PullRequest)] = struct{}{}
			case "closed":
				log.Printf("pr %+v", e.PullRequest)
				if e.PullRequest.GetMerged() {
					eventSets.merged[getURL(e.PullRequest)] = struct{}{}
				} else {
					eventSets.abandoned[getURL(e.PullRequest)] = struct{}{}
				}
			case "reopened":
				eventSets.inProgress[getURL(e.PullRequest)] = struct{}{}
			default:
				log.Printf("Unknown pull request action: %s", e.GetAction())
			}
		case *github.PullRequestReviewCommentEvent:
			eventSets.addName(e.PullRequest)
			if e.PullRequest.User.GetLogin() == *user {
				eventSets.underReview[getURL(e.PullRequest)] = struct{}{}
			} else {
				eventSets.reviewed[getURL(e.PullRequest)] = struct{}{}
			}
		// Everything below this line is ignored for now.
		case *github.CommitCommentEvent:
			log.Printf("Hit a commitCommentEvent")
		case *github.CreateEvent:
			// Probably not much for now.
			if *e.RefType == "branch" {

			} else if *e.RefType == "tag" {

			}
		case *github.PushEvent:
			// Ignore.
		case *github.DeleteEvent:
			// Ignore.
		default:
			log.Printf("Hit some other event type: %T", event)
		}
	}
	eventSets.cleanUp()
	return eventSets
}

func (e *eventSets) cleanUp() {
	// Anything that has been merged or cleaned up is no longer in progress or under review.
	for pr := range e.merged {
		delete(e.underReview, pr)
		delete(e.inProgress, pr)
	}
	for pr := range e.abandoned {
		delete(e.underReview, pr)
		delete(e.inProgress, pr)
	}
	for pr := range e.underReview {
		delete(e.inProgress, pr)
	}
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

func (e *eventSets) addName(n nameable) {
	url := url(n.GetHTMLURL())
	// Since we iterate in reverse chronological order, the first entry, should be the most
	// up-to-date.
	if _, ok := e.names[url]; !ok {
		e.names[url] = fmt.Sprintf("[%s](%s)", n.GetTitle(), n.GetHTMLURL())
		log.Printf("Added name: %q %q", url, e.names[url])
	}
}

func getURL(n nameable) url {
	return url(n.GetHTMLURL())
}
