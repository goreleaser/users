package main

import (
	"context"
	"fmt"
	"os"
	"sort"

	"golang.org/x/oauth2"

	"github.com/apex/log"
	"github.com/google/go-github/github"
)

type Repo struct {
	Name  string
	Stars int
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
		var opts = &github.SearchOptions{
			ListOptions: github.ListOptions{
				Page: 1,
			},
		}
		for {
			result, resp, err := client.Search.Code(ctx, fmt.Sprintf("filename:%s", file), opts)
			if err != nil {
				log.WithError(err).Fatal("failed to gather results")
			}
			for _, result := range result.CodeResults {
				repo, _, err := client.Repositories.Get(
					ctx,
					result.Repository.Owner.GetLogin(),
					result.Repository.GetName(),
				)
				if err != nil {
					log.WithError(err).Error("failed to get repo data")
				}
				repos = append(
					repos,
					Repo{
						Name:  result.Repository.GetFullName(),
						Stars: repo.GetStargazersCount(),
					},
				)
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
	repos = removeDupes(repos)
	for _, repo := range repos {
		log.Infof("%s has %d stars", repo.Name, repo.Stars)
	}
}

func removeDupes(repos []Repo) (result []Repo) {
	for _, r := range repos {
		if !exists(r, result) {
			result = append(result, r)
		}
	}
	return
}

func exists(r Repo, rs []Repo) bool {
	for _, rr := range rs {
		if rr.Name == r.Name {
			return true
		}
	}
	return false
}
