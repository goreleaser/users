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
	log.SetHandler(cli.New(os.Stdout))
}

func main() {
	log.Info("starting up...")
	var ctx = context.Background()
	var ts = oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: os.Getenv("GITHUB_TOKEN")},
	)
	var client = github.NewClient(oauth2.NewClient(ctx, ts))
	var repos []Repo

	for _, file := range []string{"goreleaser.yml", "goreleaser.yaml"} {
		log.Infof("looking for repos with a %s file...", file)
		var opts = &github.SearchOptions{
			ListOptions: github.ListOptions{
				Page:    1,
				PerPage: 100,
			},
		}
		for {
			result, resp, err := client.Search.Code(
				ctx,
				fmt.Sprintf("filename:%s language:yaml", file),
				opts,
			)
			if _, ok := err.(*github.RateLimitError); ok {
				log.Warn("hit rate limit")
				time.Sleep(10 * time.Second)
				continue
			}
			if err != nil {
				log.WithError(err).Fatal("failed to gather results")
			}
			log.Infof("found %d results", len(result.CodeResults))
			for _, result := range result.CodeResults {
				if exists(result.Repository.GetFullName(), repos) {
					continue
				}
				repo, err := newRepo(ctx, client, result)
				if err != nil {
					log.WithField("repo", result.Repository.GetFullName()).
						WithError(err).Error("failed to get repo details")
				}
				if repo.Name == "" {
					continue
				}
				repos = append(repos, repo)
			}
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
	write("repo;stars")
	for _, repo := range repos {
		write(fmt.Sprintf("%s;%d", repo.Name, repo.Stars))
		log.Infof("%s with %d stars (using since %v)", repo.Name, repo.Stars, repo.Date)
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
	if _, ok := err.(*github.RateLimitError); ok {
		log.Warn("hit rate limit")
		time.Sleep(10 * time.Second)
		return newRepo(ctx, client, result)
	}
	if err != nil {
		return Repo{}, err
	}
	if strings.HasPrefix(result.GetPath(), "/") {
		return Repo{}, nil
	}
	commits, _, err := client.Repositories.ListCommits(
		ctx,
		repo.Owner.GetLogin(),
		repo.GetName(),
		&github.CommitsListOptions{
			Path: result.GetPath(),
		},
	)
	if _, ok := err.(*github.RateLimitError); ok {
		log.Warn("hit rate limit")
		time.Sleep(10 * time.Second)
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
	if _, ok := err.(*github.RateLimitError); ok {
		log.Warn("hit rate limit")
		time.Sleep(10 * time.Second)
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
