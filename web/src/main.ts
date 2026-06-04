import { createApp } from 'vue'
import { createPinia } from 'pinia'
import App from './App.vue'
import { router } from './router'
import { useThemeStore } from './stores/theme'
import './style.css'

const app = createApp(App)
app.use(createPinia())
app.use(router)

// Apply the persisted theme BEFORE the first paint so users never see a
// flash of the wrong palette on reload.
useThemeStore().applyOnStartup()

app.mount('#app')
