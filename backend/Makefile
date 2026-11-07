.PHONY: test vet css run

test:        ## all tests with the race detector
	go test -race ./...

vet:
	go vet ./...

css:         ## rebuild the admin Tailwind CSS (run after editing internal/admin/templates/*.html or static/js/admin.js)
	./internal/admin/build-css.sh

run:
	go run ./cmd/movex
