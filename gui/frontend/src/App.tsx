import Home from '@/views/Home'
import {Toaster} from "@/components/ui/sonner"
import {ThemeProvider} from "@/components/theme-provider"

export default function App() {
  return (
    <ThemeProvider defaultTheme="light" storageKey="vite-ui-theme">
      <div className="h-screen">
        <Home/>
      </div>
      <Toaster duration={2000}  />
    </ThemeProvider>
  )
}