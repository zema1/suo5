import {Input} from "@/components/ui/input";
import {Button} from "@/components/ui/button";
import {RadioGroup, RadioGroupItem} from "@/components/ui/radio-group";
import {CircleStop, FolderInput, Import, Loader, Settings, Zap} from "lucide-react";
import AdvancedOption from "@/views/Advanced";
import AlertMessage from "@/views/AlertMessage";
import {useEffect, useState} from "react";
import {suo5} from "../../wailsjs/go/models";
import {DefaultSuo5Config, ExportConfig, ImportConfig, RunSuo5WithConfig, Stop} from "../../wailsjs/go/main/App";
import {Form, FormControl, FormField, FormItem, FormLabel,} from "@/components/ui/form"
import {toast} from "sonner"

import {zodResolver} from "@hookform/resolvers/zod"
import {z} from "zod"
import {useForm} from "react-hook-form"
import {EventsOff, EventsOn} from "../../wailsjs/runtime";
import {ConnectStatus, Feature} from "@/views/types";
import LogView from "@/views/Log";
import Footer from "@/views/Footer";
import {Select, SelectContent, SelectItem, SelectTrigger, SelectValue} from "@/components/ui/select";

export default function Home() {
  const [openAdvanced, setOpenAdvanced] = useState(false);
  const [config, setConfig] = useState<suo5.Suo5Config>();
  const [status, setStatus] = useState<ConnectStatus>(ConnectStatus.INITIAL);
  const [errorMessage, setErrorMessage] = useState<string>('');
  const [successMessage, setSuccessMessage] = useState<string>('');

  const FormSchema = z.object({
    method: z.string().min(1),
    target: z.string().min(1),
    listen: z.string().min(1),
    username: z.string(),
    password: z.string(),
    mode: z.string().min(1),
    feature: z.nativeEnum(Feature),
    forward_target: z.string(),
  }).superRefine((data, ctx) => {
    if (data.feature === Feature.FORWARD && !data.forward_target) {
      ctx.addIssue({
        code: z.ZodIssueCode.custom,
        path: ['forward_target'],
      })
    }
  })

  const form = useForm<z.infer<typeof FormSchema>>({
    resolver: zodResolver(FormSchema),
    defaultValues: {
      method: "",
      target: "",
      listen: "",
      username: "",
      password: "",
      mode: "auto",
      feature: Feature.SOCKS5,
      forward_target: "",
    }
  })

  // 切换功能时，清空转发地址
  const watchedFeature = form.watch('feature')
  useEffect(() => {
    form.setValue('forward_target', '')
  }, [watchedFeature]);

  const updateFormValue = (new_config: suo5.Suo5Config) => {
    Object.keys(FormSchema.innerType().shape).forEach((key) => {
      // @ts-ignore
      form.setValue(key, new_config[key]);
    })
    if (new_config.forward_target?.length > 0) {
      form.setValue('feature', Feature.FORWARD)
    } else {
      form.setValue('feature', Feature.SOCKS5)
    }
  }

  const onConnecting = async (data: z.infer<typeof FormSchema>) => {
    setSuccessMessage('')
    setErrorMessage('')
    setStatus(ConnectStatus.CONNECTING)

    await new Promise(r => setTimeout(r, 1000));

    const finial_config: suo5.Suo5Config = Object.assign({}, config, data)
    setConfig(_ => finial_config)
    await RunSuo5WithConfig(finial_config)
  }

  const onConnectSuccess = (e: string) => {
    setStatus(ConnectStatus.SUCCESS)
    setConfig(pre => {
      if (!pre) {
        return pre
      }
      let mode = "全双工"
      if (e == "half") {
        mode = "半双工"
      } else if (e == "classic") {
        mode = "短连接"
      }

      if (pre.forward_target) {
        setSuccessMessage(`连接成功，当前工作在 ${mode} 模式，${pre.listen} => ${pre.forward_target}`)
      } else {
        let proxy = ""
        if (!pre.username && !pre.password) {
          proxy = `socks5://${pre.listen}`
        } else {
          proxy = `socks5://${pre.username}:${pre.password}@${pre.listen}`
        }
        setSuccessMessage(`连接成功，当前工作在 ${mode} 模式，代理地址: ${proxy}`)
      }

      return {...pre, mode: e}
    })

  }

  const onConnectError = (e: string) => {
    setStatus(ConnectStatus.FAILED)
    setErrorMessage(`连接失败, ${e}`)
  }

  const onStop = async (e) => {
    e.preventDefault()
    await Stop()
    setStatus(ConnectStatus.INITIAL)
  }

  const onOpenAdvanced = (e) => {
    e.preventDefault()
    setOpenAdvanced(true)
  }

  const onAdvancedSubmit = async (data: suo5.Suo5Config) => {
    setConfig(data)
    setOpenAdvanced(false)
  }

  const onImportConfig = async (e) => {
    e.preventDefault()
    try {
      let new_config = await ImportConfig()
      if (new_config) {
        setConfig(new_config)
        updateFormValue(new_config)
        toast.success('导入成功')
      }
    } catch (e) {
      toast.error(`导入失败 ${e}`)
    }
  }

  const onExportConfig = async (e) => {
    e.preventDefault()

    const currentFormValues = form.getValues();
    const configToExport = Object.assign({}, config || {}, currentFormValues) as suo5.Suo5Config;

    try {
      await ExportConfig(configToExport)
      toast.success('导出配置成功')
    } catch (e) {
      console.log(e)
    }
  }

  useEffect(() => {
    DefaultSuo5Config().then((defaultConfig) => {
      defaultConfig.target = 'http://localhost:8011/tomcat_test_war_exploded/s5'
      setConfig(defaultConfig)
      updateFormValue(defaultConfig)
    });

    EventsOn('connected', onConnectSuccess)
    EventsOn('error', onConnectError)

    return () => {
      EventsOff('connected', 'error')
      Stop().then()
    }
  }, []);

  return (
    <div className="flex flex-col h-full">
      <div className="flex flex-col grow min-h-0 gap-y-4 p-4">
        <Form {...form}>
          <form onSubmit={form.handleSubmit(onConnecting)}
                className="flex flex-col gap-y-4 bg-card text-card-foreground border-t border-t-border/30 rounded-md shadow p-4">
            <div className="flex gap-4">
              <FormField
                control={form.control}
                name="method"
                render={({field}) => (
                  <FormItem className="flex *:h-8 *:text-sm">
                    <FormLabel className="min-w-[36px]">地址</FormLabel>
                    <FormControl>
                      <Input className="w-[64px]" {...field} />
                    </FormControl>
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name="target"
                render={({field}) => (
                  <FormItem className="flex w-full *:h-8 *:text-sm">
                    <FormControl>
                      <Input placeholder="http(s)://xxx" {...field} />
                    </FormControl>
                  </FormItem>
                )}
              />
            </div>

            <FormField
              control={form.control}
              name="listen"
              render={({field}) => (
                <FormItem className="flex *:h-8 *:text-sm w-full">
                  <FormLabel className="min-w-[36px]">监听</FormLabel>
                  <FormControl>
                    <Input {...field}/>
                  </FormControl>
                </FormItem>
              )}
            />

            <div className="flex gap-4">
              <FormField
                control={form.control}
                name="feature"
                render={({field}) => (
                  <FormItem className="flex *:h-8 *:text-sm">
                    <FormLabel className="min-w-[36px]">功能</FormLabel>
                    <Select onValueChange={field.onChange} defaultValue={field.value}>
                      <FormControl>
                        <SelectTrigger size="sm">
                          <SelectValue/>
                        </SelectTrigger>
                      </FormControl>
                      <SelectContent>
                        <SelectItem value="socks5">开启 Socks5 服务</SelectItem>
                        <SelectItem value="forward">转发到远程地址</SelectItem>
                      </SelectContent>
                    </Select>
                  </FormItem>
                )}
              />

              {watchedFeature === 'socks5' && (
                <>
                  <FormField
                    control={form.control}
                    name="username"
                    render={({field}) => (
                      <FormItem className="flex *:h-8 *:text-sm w-full">
                        <FormControl>
                          <Input placeholder="(可选) 用户名" {...field}/>
                        </FormControl>
                      </FormItem>
                    )}
                  />

                  <FormField
                    control={form.control}
                    name="password"
                    render={({field}) => (
                      <FormItem className="flex *:h-8 *:text-sm w-full">
                        <FormControl>
                          <Input placeholder="(可选) 密码" {...field}/>
                        </FormControl>
                      </FormItem>
                    )}
                  />
                </>
              )}

              {watchedFeature === 'forward' && (
                <FormField
                  control={form.control}
                  name="forward_target"
                  render={({field}) => (
                    <FormItem className="flex *:h-8 *:text-sm w-full">
                      <FormControl>
                        <Input placeholder="远程可访问的地址，如 10.10.10.1:22" {...field}/>
                      </FormControl>
                    </FormItem>
                  )}
                />
              )}
            </div>

            <div className="flex gap-4">
              <FormField
                control={form.control}
                name="mode"
                render={({field}) => (
                  <FormItem className="flex *:h-8 *:text-sm w-full">
                    <FormLabel className="min-w-[36px]">模式</FormLabel>
                    <FormControl>
                      <RadioGroup className="flex gap-x-4 w-full  *:h-8 *:text-sm"
                                  onValueChange={field.onChange}
                                  value={field.value}
                                  defaultValue={field.value}>
                        <FormItem className="flex items-center">
                          <FormControl>
                            <RadioGroupItem value="auto"/>
                          </FormControl>
                          <FormLabel>自动</FormLabel>
                        </FormItem>
                        <FormItem className="flex items-center">
                          <FormControl>
                            <RadioGroupItem value="full"/>
                          </FormControl>
                          <FormLabel>全双工</FormLabel>
                        </FormItem>
                        <FormItem className="flex items-center">
                          <FormControl>
                            <RadioGroupItem value="half"/>
                          </FormControl>
                          <FormLabel>半双工</FormLabel>
                        </FormItem>
                        <FormItem className="flex items-center">
                          <FormControl>
                            <RadioGroupItem value="classic"/>
                          </FormControl>
                          <FormLabel>短连接</FormLabel>
                        </FormItem>
                      </RadioGroup>
                    </FormControl>
                  </FormItem>
                )}
              />
            </div>

            <div className="flex justify-between gap-x-4 mt-2">
              <div className="flex gap-x-4">
                <Button variant="secondary" onClick={onImportConfig}><Import/>导入配置</Button>
                <Button variant="secondary" onClick={onExportConfig}><FolderInput/>导出配置</Button>
                <Button variant="secondary" onClick={onOpenAdvanced}><Settings/>高级选项</Button>
              </div>

              {status === ConnectStatus.CONNECTING &&
                  <Button onClick={onStop}><Loader className="animate-spin"/>连接中</Button>
              }

              {status === ConnectStatus.SUCCESS &&
                  <Button variant="default" onClick={onStop}><CircleStop/>停止连接</Button>
              }

              {(status === ConnectStatus.INITIAL || status === ConnectStatus.FAILED) &&
                  <Button><Zap/>立即链接</Button>
              }
            </div>
          </form>
        </Form>

        <AlertMessage status={status} successMessage={successMessage} errorMessage={errorMessage}/>
        <LogView status={status}/>

      </div>

      <Footer className="border-t px-4 py-2"/>

      <AdvancedOption open={openAdvanced} onClose={() => setOpenAdvanced(false)} config={config}
                      onSubmit={onAdvancedSubmit}/>
    </div>
  )
}