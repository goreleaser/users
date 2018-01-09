package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/apex/log"
	"github.com/apex/log/handlers/cli"
	"github.com/google/go-github/github"
	chart "github.com/wcharczuk/go-chart"
	"golang.org/x/oauth2"
)

// Repo is not actually a repo anymore, FIXME
type Repo struct {
	Name  string
	Stars int
	Date  time.Time
}

func init() {
	log.SetHandler(cli.Default)
}

func main() {
	log.Info("starting up...")
	var ctx = context.Background()
	var ts = oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: os.Getenv("GITHUB_TOKEN")},
	)
	var client = github.NewClient(oauth2.NewClient(ctx, ts))

	var repos []Repo
	var lock sync.Mutex

	for _, file := range []string{"goreleaser.yml", "goreleaser.yaml"} {
		log.Infof("looking for repos with a %s file...", file)
		var opts = &github.SearchOptions{
			ListOptions: github.ListOptions{
				Page:    1,
				PerPage: 10,
			},
		}
		for {
			result, resp, err := client.Search.Code(
				ctx,
				fmt.Sprintf("filename:%s language:yaml", file),
				opts,
			)
			if rateLimited(err) {
				continue
			}
			if err != nil {
				log.WithError(err).Fatal("failed to gather results")
			}
			log.Infof("found %d results", len(result.CodeResults))
			var wg sync.WaitGroup
			wg.Add(len(result.CodeResults))
			for _, result := range result.CodeResults {
				result := result
				go func() {
					defer wg.Done()
					var log = log.WithField("repo", result.Repository.GetFullName())
					lock.Lock()
					if exists(result.Repository.GetFullName(), repos) {
						lock.Unlock()
						log.Warn("already exist")
						return
					}
					lock.Unlock()
					repo, err := newRepo(ctx, client, result)
					if err != nil {
						log.
							WithError(err).
							Warn("failed to get repo details, discard")
						return
					}
					lock.Lock()
					repos = append(repos, repo)
					lock.Unlock()
				}()
			}
			wg.Wait()
			if resp.NextPage == 0 {
				break
			}
			opts.Page = resp.NextPage
		}
	}
	sort.Slice(repos, func(i, j int) bool {
		return repos[i].Stars > repos[j].Stars
	})
	log.Info("")
	log.Info("")
	log.Infof("\033[1mTHERE ARE %d REPOSITORIES USING GORELEASER:\033[0m", len(repos))
	log.Info("")
	var csv = fmt.Sprintf("data/%s.csv", time.Now().Format("20060102"))
	f, err := os.OpenFile(csv, os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0644)
	if err == nil {
		defer func() {
			if err := f.Close(); err != nil {
				log.WithField("file", csv).WithError(err).Error("failed to close file")
			}
		}()
	} else {
		log.WithField("file", csv).WithError(err).Fatal("failed create data file")
	}
	w := bufio.NewWriter(f)
	defer func() {
		if err := w.Flush(); err != nil {
			log.WithField("file", csv).WithError(err).Error("failed to write to file")
		}
	}()
	write := func(s string) {
		if err != nil {
			return
		}
		_, err = w.WriteString(s + "\n")
	}
	write("repo,stars")
	for _, repo := range repos {
		write(fmt.Sprintf("%s,%d", repo.Name, repo.Stars))
	}
	if err != nil {
		log.WithField("file", csv).WithError(err).Fatal("failed write to data file")
	}
	log.Info("")
	log.Info("")
	log.Infof("\033[1mGRAPHS GENERATED:\033[0m")
	graph, err := graphRepos(repos)
	if err != nil {
		log.WithError(err).Fatal("failed to graph repos")
	}
	log.Info(graph)
	graph, err = graphRepoStars(repos)
	if err != nil {
		log.WithError(err).Fatal("failed to graph repo stars")
	}
	log.Info(graph)
}

func newRepo(ctx context.Context, client *github.Client, result github.CodeResult) (Repo, error) {
	repo, _, err := client.Repositories.Get(
		ctx,
		result.Repository.Owner.GetLogin(),
		result.Repository.GetName(),
	)
	if rateLimited(err) {
		return newRepo(ctx, client, result)
	}
	if err != nil {
		return Repo{}, err
	}
	if strings.Contains(result.GetPath(), "vendor") {
		return Repo{}, fmt.Errorf("invalid file location: %s", result.GetPath())
	}
	commits, _, err := client.Repositories.ListCommits(
		ctx,
		repo.Owner.GetLogin(),
		repo.GetName(),
		&github.CommitsListOptions{
			Path: result.GetPath(),
		},
	)
	if rateLimited(err) {
		return newRepo(ctx, client, result)
	}
	if err != nil || len(commits) == 0 {
		return Repo{}, err
	}
	commit := commits[len(commits)-1]
	c, _, err := client.Git.GetCommit(
		ctx,
		repo.Owner.GetLogin(),
		repo.GetName(),
		commit.GetSHA(),
	)
	if rateLimited(err) {
		return newRepo(ctx, client, result)
	}
	if err != nil {
		return Repo{}, err
	}

	return Repo{
		Name:  repo.GetFullName(),
		Stars: repo.GetStargazersCount(),
		Date:  c.Committer.GetDate(),
	}, nil
}

func exists(name string, rs []Repo) bool {
	for _, r := range rs {
		if r.Name == name {
			return true
		}
	}
	return false
}

func graphRepoStars(repos []Repo) (string, error) {
	var filename = "stars.png"
	var graph = chart.BarChart{
		Title:      "Top 5 repositories using GoReleaser by number of stargazers",
		TitleStyle: chart.StyleShow(),
		XAxis:      chart.StyleShow(),
		YAxis: chart.YAxis{
			Style:     chart.StyleShow(),
			NameStyle: chart.StyleShow(),
		},
	}
	sort.Slice(repos, func(i, j int) bool {
		return repos[i].Stars > repos[j].Stars
	})
	for i, repo := range repos {
		if i > 5 {
			break
		}
		graph.Bars = append(graph.Bars, chart.Value{
			Value: float64(repo.Stars),
			Label: repo.Name,
		})
	}
	var buffer = bytes.NewBuffer([]byte{})
	if err := graph.Render(chart.PNG, buffer); err != nil {
		return "", err
	}
	if err := ioutil.WriteFile(filename, buffer.Bytes(), 0644); err != nil {
		return "", err
	}
	return filename, nil
}

func graphRepos(repos []Repo) (string, error) {
	var filename = "repos.png"
	var series = chart.TimeSeries{Style: chart.StyleShow()}
	sort.Slice(repos, func(i, j int) bool {
		return repos[i].Date.Before(repos[j].Date)
	})
	for i, repo := range repos {
		series.XValues = append(series.XValues, repo.Date)
		series.YValues = append(series.YValues, float64(i))
	}
	var graph = chart.Chart{
		Title:      "Number of repositories using GoReleaser over time",
		TitleStyle: chart.StyleShow(),
		XAxis: chart.XAxis{
			Name:      "Time",
			NameStyle: chart.StyleShow(),
			Style:     chart.StyleShow(),
		},
		YAxis: chart.YAxis{
			Name:      "Repositories",
			NameStyle: chart.StyleShow(),
			Style:     chart.StyleShow(),
		},
		Series: []chart.Series{series},
	}
	var buffer = bytes.NewBuffer([]byte{})
	if err := graph.Render(chart.PNG, buffer); err != nil {
		return "", err
	}
	if err := ioutil.WriteFile(filename, buffer.Bytes(), 0644); err != nil {
		return "", err
	}
	return filename, nil
}

func rateLimited(err error) bool {
	rerr, ok := err.(*github.RateLimitError)
	if ok {
		var d = rerr.Rate.Reset.Time.Sub(time.Now())
		log.Warnf("hit rate limit, sleeping for %d min", d.Minutes())
		time.Sleep(d)
		return true
	}
	aerr, ok := err.(*github.AbuseRateLimitError)
	if ok {
		var d = aerr.GetRetryAfter()
		log.Warnf("hit abuse mechanism, sleeping for %d min", d.Minutes())
		time.Sleep(d)
		return true
	}
	return false
}
