import { pathToRoot } from "../util/path"
import { QuartzComponent, QuartzComponentConstructor, QuartzComponentProps } from "./types"
import { classNames } from "../util/lang"

const Logo: QuartzComponent = ({ fileData, cfg, displayClass }: QuartzComponentProps) => {
  const baseDir = pathToRoot(fileData.slug!)
  return (
    <a href={baseDir} class={classNames(displayClass, "page-title")}>
      <i class="logo"></i>
    </a>
  )
}

Logo.css = `
.page-title {
  margin: 0;
}

.logo {
  width: 80px;
  height: 60px; 
  background-color: var(--tertiary);
  -webkit-mask: url("static/logo.svg") no-repeat center / contain;
          mask: url("static/logo.svg") no-repeat center / contain;
  display: inline-block;
}

`

export default (() => Logo) satisfies QuartzComponentConstructor
