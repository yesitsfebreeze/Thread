


create_docs:
	@rm -rf quartz/content
	@mkdir -p quartz/content
	@cp -R obsidian/* quartz/content
	@cd quartz && npx quartz create

docs:
	@cd quartz && npx quartz build --serve
