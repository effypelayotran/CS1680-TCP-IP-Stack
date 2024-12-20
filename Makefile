all:
	go build -o vhost ./cmd/vhost
	go build -o vrouter ./cmd/vrouter

clean:
	rm -f vhost vrouter