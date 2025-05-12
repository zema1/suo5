import {Trash2} from "lucide-react";
import {ScrollArea, ScrollBar} from "@/components/ui/scroll-area.tsx";
import {Light as SyntaxHighlighter} from "react-syntax-highlighter";
import accesslog from 'react-syntax-highlighter/dist/esm/languages/hljs/accesslog';
import {atomOneDark, atomOneLight} from "react-syntax-highlighter/dist/esm/styles/hljs"
import {ComponentProps, useEffect, useMemo, useRef, useState} from "react";
import {useTheme} from "@/components/theme-provider";
import {EventsOff, EventsOn} from "../../wailsjs/runtime";
import {ConnectStatus} from "@/views/types.ts";
import {cn} from "@/lib/utils.ts";

SyntaxHighlighter.registerLanguage('accesslog', accesslog);

interface LogViewProps {
  status: ConnectStatus
}

export default function LogView({status, className, ...props}: ComponentProps<'div'> & LogViewProps) {
  const [logValue, setLogValue] = useState<string>('');
  const logCount = useRef(0);
  const {theme} = useTheme()

  // 改一下默认的 bg 样式
  const highlighterStyle = useMemo(() => {
    const baseTheme = theme === 'light' ? atomOneLight : atomOneDark;
    const mainStyleKey = 'hljs';
    return {
      ...baseTheme,
      [mainStyleKey]: {
        ...(baseTheme[mainStyleKey] || {}),
        background: 'inherit', // 或者 'transparent'
        padding: 0,
      },
    };
  }, [theme]);

  // logValue 改变时，自动滚动到底部
  const scrollEndRef = useRef(null);
  useEffect(() => {
    if (scrollEndRef.current) {
      scrollEndRef.current.scrollIntoView({behavior: 'auto', block: 'end'});
    }
  }, [logValue]);


  const onLogValue = (e: string) => {
    // 防止日志太多 OOM
    logCount.current += 1
    if (logCount.current == 1000) {
      logCount.current = 0
      setLogValue('')
    }
    setLogValue((prev) => `${prev}${e}`)
  }

  useEffect(() => {
    EventsOn('log', onLogValue)
    return () => {
      EventsOff('log')
    }
  }, []);

  useEffect(() => {
    if (status === ConnectStatus.CONNECTING) {
      setLogValue('')
    }
  }, [status]);

  const baseClass = "flex flex-1 flex-col space-y-4 bg-muted/50 border border-border/50 rounded [&>p]:m-0 [&>p]:text-sm min-h-0"

  return (
    <div
      className={cn(baseClass, className)} {...props}>
      <div className="flex justify-between items-center shrink-0 px-4 pt-4">
        <p className="text-sm">运行日志</p>
        <Trash2 size={18} strokeWidth={1.6} onClick={() => setLogValue('')}/>
      </div>
      <ScrollArea className="flex flex-1 min-h-0 text-muted-foreground text-sm px-4 pb-4">
        <SyntaxHighlighter
          children={logValue}
          style={highlighterStyle}
          useInlineStyles={true}
          language="accesslog"
        />
        <div ref={scrollEndRef}/>
        <ScrollBar orientation="horizontal"/>
      </ScrollArea>
    </div>
  )
}