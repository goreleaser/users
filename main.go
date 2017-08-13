package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/apex/log"
	"github.com/apex/log/handlers/cli"
	"github.com/google/go-github/github"
	"golang.org/x/oauth2"
)

type Repo struct {
	Name  string
	Stars int
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

	for _, file := range []string{"goreleaser.yml", ".goreleaser.yml"} {
		log.Infof("looking for repos with a %s file...", file)
		var opts = &github.SearchOptions{
			ListOptions: github.ListOptions{
				Page:    1,
				PerPage: 100,
			},
		}
		for {
			result, resp, err := client.Search.Code(ctx, fmt.Sprintf("filename:%s", file), opts)
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
				if !exists(result.Repository.GetFullName(), repos) {
					repo, err := newRepo(ctx, client, result)
					if err != nil {
						log.WithField("repo", result.Repository.GetFullName()).
							WithError(err).Error("failed to get repo details")
					}
					repos = append(repos, repo)
				}
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
	for _, repo := range repos {
		log.Infof("%s has %d stars", repo.Name, repo.Stars)
	}
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
	return Repo{
		Name:  repo.GetFullName(),
		Stars: repo.GetStargazersCount(),
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
