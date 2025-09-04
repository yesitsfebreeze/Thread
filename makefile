.PHONY: docs dev



docs: clean_docs
	@git clone https://github.com/jackyzha0/quartz.git ./tmp
	@rm -rf tmp/content
	@mkdir -p tmp/content
	@cp -R quartz/* tmp/
	@cp -R obsidian/* tmp/content
	@mv tmp/content/assets tmp/content/static
	
	@cd tmp && npm i && npx quartz create -d ./content -X "new" -l "shortest"
	@mv tmp/content/__index__.md tmp/content/index.md
	@cd tmp && npx quartz build
	@mkdir -p ./docs
	@cp tmp/public/* -R ./docs

dev:
	@cd tmp && npx quartz build --serve


build:
	@cd server && go mod tidy
	@cd server && go build -o ../bin/thread

watch:
	@go install github.com/air-verse/air@latest
ifeq ($(OS),Windows_NT)
	@cd server && air -c .air.win.toml
else
	@cd server && air -c .air.lin.toml
endif


clean_docs:
	@echo "Cleaning up docs..."
	@rm -rf tmp
	@rm -rf docs

clean_bin:
	@rm -rf bin

clean: clean_docs clean_bin
	