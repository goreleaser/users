package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"sort"
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

	var repos sync.Map

	for _, ext := range []string{"yml", "yaml"} {
		log.Infof("looking for repos with a goreleaser %s file...", ext)
		var opts = &github.SearchOptions{
			ListOptions: github.ListOptions{
				Page:    1,
				PerPage: 30,
			},
		}
		for {
			result, resp, err := client.Search.Code(
				ctx,
				fmt.Sprintf("filename:goreleaser extension:%s path:/", ext),
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
					key := result.Repository.GetFullName()
					var log = log.WithField("repo", key)
					if _, ok := repos.Load(key); ok {
						log.Info("already in the list")
						return
					}
					repo, err := newRepo(ctx, client, result)
					if err != nil {
						log.WithError(err).Warn("ignoring")
						return
					}
					repos.Store(key, repo)
				}()
			}
			wg.Wait()
			if resp.NextPage == 0 {
				break
			}
			opts.Page = resp.NextPage
		}
	}
	var repoSlice []Repo
	repos.Range(func(key, value interface{}) bool {
		repoSlice = append(repoSlice, value.(Repo))
		return true
	})
	sort.Slice(repoSlice, func(i, j int) bool {
		return repoSlice[i].Stars > repoSlice[j].Stars
	})
	log.Info("")
	log.Info("")
	log.Infof("\033[1mthere are %d repositories using goreleaser\033[0m", len(repoSlice))
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
	write("repo,stars,since")
	for _, repo := range repoSlice {
		write(fmt.Sprintf("%s,%d,%s", repo.Name, repo.Stars, repo.Date))
	}
	if err != nil {
		log.WithField("file", csv).WithError(err).Fatal("failed write to data file")
	}
	log.Info("")
	log.Info("")
	log.Infof("\033[1mgraphs generated:\033[0m")
	graph, err := graphRepos(repoSlice)
	if err != nil {
		log.WithError(err).Fatal("failed to graph repos")
	}
	log.Info(graph)
	graph, err = graphRepoStars(repoSlice)
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
	if err != nil {
		return Repo{}, err
	}
	if len(commits) == 0 {
		return Repo{}, fmt.Errorf("no commits found for %s", result.GetRepository().GetFullName())
	}
	commit := commits[len(commits)-1]
	return Repo{
		Name:  repo.GetFullName(),
		Stars: repo.GetStargazersCount(),
		Date:  commit.GetCommit().GetCommitter().GetDate(),
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
	for _, repo := range repos[:5] {
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
	if err == nil {
		return false
	}
	rerr, ok := err.(*github.RateLimitError)
	if ok {
		var d = rerr.Rate.Reset.Time.Sub(time.Now())
		log.Warnf("hit rate limit, sleeping for %.0f min", d.Minutes())
		time.Sleep(d)
		return true
	}
	aerr, ok := err.(*github.AbuseRateLimitError)
	if ok {
		var d = aerr.GetRetryAfter()
		log.Warnf("hit abuse mechanism, sleeping for %.f min", d.Minutes())
		time.Sleep(d)
		return true
	}
	log.WithError(err).Errorf("wtf")
	return false
}
