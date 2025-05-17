import {Moon, Sun} from "lucide-react"
import {useTheme} from "@/components/theme-provider"
import {useRef} from "react";
import {Button} from "@/components/ui/button.tsx";

export function ThemeToggle() {
  const {setTheme} = useTheme()
  const theme = useRef('light');
  const toggleTheme = () => {
    if (theme.current === 'light') {
      setTheme('dark');
      theme.current = 'dark';
    } else {
      setTheme('light');
      theme.current = 'light';
    }
  }

  return (
    <Button size="icon" variant="ghost" onClick={toggleTheme} className="size-[20px] transition-none">
      {theme.current === "dark" ? <Sun/> : <Moon/>}
    </Button>
  )
}
