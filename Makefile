run:
	go run main.go
	git add -A
	git commit -m 'bump data'
	git push origin master
.PHONY: run

.DEFAULT_GOAL := run
