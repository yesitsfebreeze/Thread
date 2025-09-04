.PHONY: docs dev

docs:
	@rm -rf tmp
	@rm -rf pages
	@git clone https://github.com/jackyzha0/quartz.git ./tmp
	@rm -rf tmp/content
	@mkdir -p tmp/content
	@cp -R obsidian/* tmp/content
	@cp -R quartz.config.ts tmp/quartz.config.ts
	@mv tmp/content/__index__.md tmp/content/index.md
	@cd tmp && npm i && npx quartz create -d ./content -X "new" -l "shortest"
	@cd tmp && npx quartz build
	@mkdir -p ./pages
	@cp tmp/public/* -R ./pages
	@rm -rf tmp

dev:
	@cd tmp && npx quartz build --serve
