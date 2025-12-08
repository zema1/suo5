import {ThemeToggle} from "@/components/theme-toggle.tsx";
import * as React from "react";
import {useEffect, useState} from "react";
import {main} from "../../wailsjs/go/models.ts";
import {EventsOff, EventsOn} from "../../wailsjs/runtime";
import {MoveDown, MoveUp, Network} from "lucide-react";
import {cn} from "@/lib/utils.ts";


export default function Footer({className, ...props}: React.ComponentProps<'div'>) {
  const [runStatus, setRunStatus] = useState<main.RunStatus>({download: "", upload: "", connection_count: 0});

  useEffect(() => {
    EventsOn('status', (e: main.RunStatus) => {
      setRunStatus(e)
    })

    return () => {
      EventsOff('status')
    }
  }, []);

  const baseClass = "flex justify-between items-center text-sm gap-4 *:flex *:items-center *:gap-1"

  return (
    <div className={cn(baseClass, className)} {...props}>
      <div>
        <Network size={12}/>
        <p className="min-w-12">{runStatus.connection_count}</p>
      </div>
      <div>
        <MoveUp size={12}/>
        <p className="min-w-12">{runStatus.upload}</p>
      </div>
      <div>
        <MoveDown size={12}/>
        <p className="min-w-12">{runStatus.download}</p>
      </div>
      <div><ThemeToggle/></div>
    </div>
  )
}