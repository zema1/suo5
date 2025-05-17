import {Alert, AlertTitle} from "@/components/ui/alert";
import {CircleCheckBig, PlugZap, ZapOff} from "lucide-react";
import {ConnectStatus} from "@/views/types";

interface AlertMessageProps {
  status: ConnectStatus
  successMessage?: string,
  errorMessage?: string,
}

export default function AlertMessage({
                                       status,
                                       successMessage = "",
                                       errorMessage = "",
                                     }: AlertMessageProps) {
  switch (status) {
    case ConnectStatus.INITIAL:
      return (
        <Alert className="border-0 bg-muted">
          <PlugZap/>
          <AlertTitle>
            等待发起连接
          </AlertTitle>
        </Alert>
      )
    case ConnectStatus.CONNECTING:
      return (
        <Alert className="border-0 bg-muted">
          <PlugZap className="animate-pulse"/>
          <AlertTitle>
            连接中...
          </AlertTitle>
        </Alert>
      )
    case ConnectStatus.SUCCESS:
      return (
        <Alert className="border-0 bg-emerald-500/10 dark:bg-emerald-600/30">
          <CircleCheckBig className="stroke-emerald-500"/>
          <AlertTitle>
            {successMessage}
          </AlertTitle>
        </Alert>
      )
    case ConnectStatus.FAILED:
      return (
        <Alert className="border-0 bg-amber-500/10 dark:bg-amber-600/30">
          <ZapOff className="stroke-amber-500"/>
          <AlertTitle>
            {errorMessage}
          </AlertTitle>
        </Alert>
      )
  }
}
