.PHONY: docs dev

docs:
	@rm -rf tmp
	@rm -rf pages
	@git clone https://github.com/jackyzha0/quartz.git ./tmp
	@rm -rf tmp/content
	@mkdir -p tmp/content
	@cp -R obsidian/* tmp/content
	@cp -R quartz.config.ts tmp/quartz.config.ts
	@cd tmp && npm i && npx quartz create -d ./content -X "new" -l "shortest"
	@mv tmp/content/__index__.md tmp/content/index.md
	@cd tmp && npx quartz build
	@mkdir -p ./docs
	@cp tmp/public/* -R ./docs

dev:
	@cd tmp && npx quartz build --serve
