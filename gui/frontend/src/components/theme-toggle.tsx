import {Moon, Sun} from "lucide-react"
import {useTheme} from "@/components/theme-provider"
import {useRef} from "react";
import {Switch} from "@/components/ui/switch";

export function ThemeToggle() {
  const {setTheme} = useTheme()
  const theme = useRef('light');
  const toggerTheme = () => {
    if (theme.current === 'light') {
      setTheme('dark');
      theme.current = 'dark';
    } else {
      setTheme('light');
      theme.current = 'light';
    }
  }

  return (
    <div className="relative inline-grid h-[20px] w-[40px] grid-cols-2">
      <Switch
        onClick={toggerTheme}
        className="absolute inset-0 h-full w-full data-[state=unchecked]:bg-input/80 [&_span]:z-10 [&_span]:h-full [&_span]:w-1/2 [&_span]:duration-300 [&_span]:data-[state=checked]:translate-x-full"
      />
      <span
        className="pointer-events-none relative pe-0.5 w-full flex items-center justify-center peer-data-[state=checked]:invisible peer-data-[state=unchecked]:translate-x-full">
        <Sun size={14} aria-hidden="true"/>
      </span>
      <span
        className="pointer-events-none relative ps-0.5 w-full flex items-center justify-center peer-data-[state=unchecked]:invisible peer-data-[state=checked]:-translate-x-full peer-data-[state=checked]:text-background">
        <Moon size={14} aria-hidden="true"/>
      </span>
    </div>
  )
}
