import { DeploymentsElement } from './element'
import styles from './style.css?raw'

const TAG = 'faros-provider-deployments'
if (!customElements.get(TAG)) {
  const styleID = `${TAG}-css`
  if (!document.getElementById(styleID)) {
    const style = document.createElement('style')
    style.id = styleID
    style.textContent = styles
    document.head.appendChild(style)
  }
  customElements.define(TAG, DeploymentsElement)
}
