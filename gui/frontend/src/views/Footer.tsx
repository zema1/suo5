import {ThemeToggle} from "@/components/theme-toggle.tsx";
import {useEffect, useState} from "react";
import {main} from "../../wailsjs/go/models.ts";
import {EventsOff, EventsOn} from "../../wailsjs/runtime";
import {MoveDown, MoveUp, Network} from "lucide-react";
import RunStatus = main.RunStatus;

export default function Footer() {
  const [runStatus, setRunStatus] = useState<main.RunStatus>({download: "", upload: "", connection_count: 0});

  useEffect(() => {
    EventsOn('status', (e: RunStatus) => {
      setRunStatus(e)
    })

    return () => {
      EventsOff('status')
    }
  }, []);

  return (
    <div className="flex justify-between items-center text-sm">
      <div className="flex gap-4 *:flex *:items-center *:gap-1">
        <div>
          <Network size={12}/>
          <p className="min-w-4">{runStatus.connection_count}</p>
        </div>
        <div>
          <MoveUp size={12}/>
          <p className="min-w-12">{runStatus.upload}</p>
        </div>
        <div>
          <MoveDown size={12}/>
          <p className="min-w-12">{runStatus.download}</p>
        </div>
      </div>
      <div><ThemeToggle/></div>
    </div>
  )
}