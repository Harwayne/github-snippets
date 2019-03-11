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

type eventSets struct {
	merged      map[string]bool
	abandoned   map[string]bool
	underReview map[string]bool
	inProgress  map[string]bool
	reviewed    map[string]bool
	issues      map[string]bool
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
		merged:      make(map[string]bool),
		abandoned:   make(map[string]bool),
		underReview: make(map[string]bool),
		inProgress:  make(map[string]bool),
		reviewed:    make(map[string]bool),
		issues:      make(map[string]bool),
	}
	for _, event := range parsed {
		switch e := event.(type) {
		case *github.CommitCommentEvent:
			log.Printf("Hit a commitCommentEvent")
		case *github.CreateEvent:
			// Probably not much for now.
			if *e.RefType == "branch" {

			} else if *e.RefType == "tag" {

			}
		case *github.IssueCommentEvent:
			if e.Issue.IsPullRequest() {
				if e.Issue.User.GetLogin() == *user {
					eventSets.underReview[issueTitle(e.Issue)] = true
				} else {
					eventSets.reviewed[issueTitle(e.Issue)] = true
				}
			} else {
				eventSets.issues[issueTitle(e.Issue)] = true
			}
		case *github.IssuesEvent:
			switch e.GetAction() {
			case "opened":
			case "edited":
			case "deleted":
			case "closed":
			case "assigned":
			}
			if e.Issue.IsPullRequest() {
				eventSets.reviewed[issueTitle(e.Issue)] = true
			} else {
				eventSets.issues[issueTitle(e.Issue)] = true
				log.Printf("Added issueevent %s", issueTitle(e.Issue))
			}
		case *github.PullRequestEvent:
			switch e.GetAction() {
			case "opened":
				if strings.Contains(e.PullRequest.GetTitle(), "WIP") {
					eventSets.inProgress[prTitle(e.PullRequest)] = true
				} else {
					eventSets.underReview[prTitle(e.PullRequest)] = true
				}
			case "edited":
				eventSets.inProgress[prTitle(e.PullRequest)] = true
			case "closed":
				log.Printf("pr %+v", e.PullRequest)
				if e.PullRequest.GetMerged() {
					eventSets.merged[prTitle(e.PullRequest)] = true
				} else {
					eventSets.abandoned[prTitle(e.PullRequest)] = true
				}
			case "reopened":
				eventSets.inProgress[prTitle(e.PullRequest)] = true
			default:
				log.Printf("Unknown pull request action: %s", e.GetAction())
			}
		case *github.PullRequestReviewCommentEvent:
			if e.PullRequest.User.GetLogin() == *user {
				eventSets.underReview[prTitle(e.PullRequest)] = true
			} else {
				eventSets.reviewed[prTitle(e.PullRequest)] = true
			}
		case *github.PushEvent:
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
	md = append(md, printSection(e.merged, "Merged")...)
	md = append(md, printSection(e.abandoned, "Abandoned")...)
	md = append(md, printSection(e.underReview, "Under Review")...)
	md = append(md, printSection(e.inProgress, "In Progress")...)
	md = append(md, printSection(e.reviewed, "Reviewed")...)
	md = append(md, printSection(e.issues, "Issues")...)

	markdown := strings.Join(md, "\n")
	markdown = strings.Replace(markdown, "\t", "    ", -1)
	return markdown
}

func printSection(section map[string]bool, title string) []string {
	if len(section) > 0 {
		md := make([]string, 0, len(section)+1)
		md = append(md, fmt.Sprintf("\t* %s", title))
		for pr := range section {
			md = append(md, fmt.Sprintf("\t\t* %s", pr))
		}
		return md
	}
	return make([]string, 0)
}

func prTitle(pr *github.PullRequest) string {
	return fmt.Sprintf("[%s](%s)", pr.GetTitle(), pr.GetHTMLURL())
}

func issueTitle(issue *github.Issue) string {
	return fmt.Sprintf("[%s](%s)", issue.GetTitle(), issue.GetHTMLURL())
}
