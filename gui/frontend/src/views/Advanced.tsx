import {Button} from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import {suo5} from "../../wailsjs/go/models"
import {Checkbox} from "@/components/ui/checkbox";
import {Input} from "@/components/ui/input";
import {Textarea} from "@/components/ui/textarea";
import {useEffect, useState} from "react";
import {CheckedState} from "@radix-ui/react-checkbox";
import {z} from "zod";
import {useForm} from "react-hook-form";
import {zodResolver} from "@hookform/resolvers/zod";
import {Form, FormControl, FormField, FormItem, FormLabel} from "@/components/ui/form";

interface AdvancedOptionsProps {
  open: boolean
  config: suo5.Suo5Config
  onClose: () => void
  onSubmit: (data: suo5.Suo5Config) => void
}


const FormSchema = z.object({
  debug: z.boolean(),
  enable_cookiejar: z.boolean(),
  disable_heartbeat: z.boolean(),
  disable_gzip: z.boolean(),
  timeout: z.coerce.number(),
  buffer_size: z.coerce.number(),
  redirect_url: z.string(),
  upstream_proxy: z.array(z.string()),
  raw_header: z.array(z.string()),
  classic_poll_qps: z.coerce.number(),
  retry_count: z.coerce.number(),

  input_headers: z.string(),
  input_upstream_proxy: z.string(),
})


export default function AdvancedOption({open, config, onClose, onSubmit}: AdvancedOptionsProps) {
  const [devMode, setDevMode] = useState<CheckedState>(false);

  const onCloseInner = (e) => {
    e.preventDefault()
    onClose?.()
  }

  const form = useForm<z.infer<typeof FormSchema>>({
    resolver: zodResolver(FormSchema),
    defaultValues: {
      debug: false,
      enable_cookiejar: false,
      disable_heartbeat: false,
      disable_gzip: false,
      timeout: 0,
      buffer_size: 0,
      redirect_url: "",
      upstream_proxy: [],
      raw_header: [],
      classic_poll_qps: 0,
      retry_count: 0,

      input_headers: "",
      input_upstream_proxy: "",
    }
  })

  useEffect(() => {
    if (!config) {
      return
    }
    let data: z.infer<typeof FormSchema> = Object.assign({}, config)
    if (data.raw_header.length > 0) {
      data.input_headers = data.raw_header.join("\n")
    } else {
      data.input_headers = ""
    }
    if (data.upstream_proxy.length > 0) {
      data.input_upstream_proxy = data.upstream_proxy.join(",")
    } else {
      data.input_upstream_proxy = ""
    }
    setDevMode(config.debug)
    form.reset(data)
  }, [config, open]);

  const handleOpenChange = (open: boolean) => {
    if (!open) {
      onClose?.()
    }
  }

  const onBeforeSubmit = (data: z.infer<typeof FormSchema>) => {
    Object.assign(config, data)
    if (data.input_headers?.trim().length > 0) {
      config.raw_header = data.input_headers.split("\n")
    }
    if (data.input_upstream_proxy?.trim().length > 0) {
      config.upstream_proxy = data.input_upstream_proxy.split(",")
    }
    console.log('submit', config)
    onSubmit(config)
  }

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent>
        <DialogHeader className="text-left">
          <DialogTitle>高级选项</DialogTitle>
          <DialogDescription></DialogDescription>
        </DialogHeader>

        <Form {...form}>
          <form onSubmit={form.handleSubmit(onBeforeSubmit)} className="flex flex-col gap-4">
            <div className="grid grid-cols-4 gap-2">
              <FormField
                control={form.control}
                name="debug"
                render={({field}) => (
                  <FormItem className="flex">
                    <FormLabel className="min-w-[80px] justify-end">调式模式</FormLabel>
                    <FormControl>
                      <Checkbox checked={field.value} onCheckedChange={(e) => {
                        field.onChange(e);
                        setDevMode(e)
                      }}/>
                    </FormControl>
                  </FormItem>
                )}
              />
              {devMode && (
                <>
                  <FormField
                    control={form.control}
                    name="enable_cookiejar"
                    render={({field}) => (
                      <FormItem className="flex">
                        <FormLabel className="min-w-[80px] justify-end">CookieJar</FormLabel>
                        <FormControl>
                          <Checkbox checked={field.value} onCheckedChange={field.onChange}/>
                        </FormControl>
                      </FormItem>
                    )}
                  />

                  <FormField
                    control={form.control}
                    name="disable_heartbeat"
                    render={({field}) => (
                      <FormItem className="flex">
                        <FormLabel className="min-w-[80px] justify-end">禁用心跳包</FormLabel>
                        <FormControl>
                          <Checkbox checked={field.value} onCheckedChange={field.onChange}/>
                        </FormControl>
                      </FormItem>
                    )}
                  />

                  <FormField
                    control={form.control}
                    name="disable_gzip"
                    render={({field}) => (
                      <FormItem className="flex">
                        <FormLabel className="min-w-[80px] justify-end">禁用 Gzip</FormLabel>
                        <FormControl>
                          <Checkbox checked={field.value} onCheckedChange={field.onChange}/>
                        </FormControl>
                      </FormItem>
                    )}
                  />
                </>
              )}
            </div>

            <div className="grid grid-cols-2 gap-2">
              <FormField
                control={form.control}
                name="retry_count"
                render={({field}) => (
                  <FormItem className="flex">
                    <FormLabel className="min-w-[80px] justify-end">请求重试</FormLabel>
                    <FormControl>
                      <Input type="number" className="h-8 text-sm" {...field}/>
                    </FormControl>
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name="timeout"
                render={({field}) => (
                  <FormItem className="flex">
                    <FormLabel className="min-w-[80px] justify-end">超时时间</FormLabel>
                    <FormControl>
                      <Input type="number" className="h-8 text-sm" {...field}/>
                    </FormControl>
                  </FormItem>
                )}
              />


            </div>

            <div className="grid grid-cols-2 gap-2">
              <FormField
                control={form.control}
                name="classic_poll_qps"
                render={({field}) => (
                  <FormItem className="flex">
                    <FormLabel className="min-w-[80px] justify-end">短连接 QPS</FormLabel>
                    <FormControl>
                      <Input type="number" className="h-8 text-sm" {...field}/>
                    </FormControl>
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name="buffer_size"
                render={({field}) => (
                  <FormItem className="flex">
                    <FormLabel className="min-w-[80px] justify-end">缓冲区大小</FormLabel>
                    <FormControl>
                      <Input type="number" className="h-8 text-sm" {...field}/>
                    </FormControl>
                  </FormItem>
                )}
              />
            </div>

            <FormField
              control={form.control}
              name="redirect_url"
              render={({field}) => (
                <FormItem className="flex">
                  <FormLabel className="min-w-[80px] justify-end">流量集中</FormLabel>
                  <FormControl>
                    <Input className="h-8 text-sm" placeholder="用于应对负载均衡, 将流量集中转发中该地址" {...field}/>
                  </FormControl>
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name="input_upstream_proxy"
              render={({field}) => (
                <FormItem className="flex">
                  <FormLabel className="min-w-[80px] justify-end">上游代理</FormLabel>
                  <FormControl>
                    <Input className="h-8 text-sm"
                           placeholder="socks5 or http(s), socks5://user:pass@ip:port" {...field}/>
                  </FormControl>
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name="input_headers"
              render={({field}) => (
                <FormItem className="flex">
                  <FormLabel className="min-w-[80px] justify-end items-start">请求头</FormLabel>
                  <FormControl>
                    <Textarea className="text-sm placeholder:text-muted-foreground/50 h-30"
                              placeholder="User-Agent: xxx" {...field}
                    />
                  </FormControl>
                </FormItem>
              )}
            />

            <DialogFooter className="flex flex-row justify-end">
              <Button variant="secondary" onClick={onCloseInner}>取消</Button>
              <Button type="submit">保存</Button>
            </DialogFooter>
          </form>
        </Form>
      </DialogContent>
    </Dialog>
  )
}