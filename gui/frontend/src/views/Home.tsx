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

export default function Home() {
  const [openAdvanced, setOpenAdvanced] = useState(false);
  const [config, setConfig] = useState<suo5.Suo5Config>();
  const [status, setStatus] = useState<ConnectStatus>(ConnectStatus.INITIAL);
  const [connectDisabled, setConnectDisabled] = useState<boolean>(false);
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
      method: "GET",
      target: "",
      listen: "127.0.0.1:1080",
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
      if (new_config[key] !== undefined) {
        // @ts-ignore
        form.setValue(key, new_config[key]);
      }
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
    setConnectDisabled(true)
    setStatus(ConnectStatus.CONNECTING)

    await new Promise(r => setTimeout(r, 1000));

    const finial_config: suo5.Suo5Config = Object.assign({}, config, data)
    setConfig(_ => finial_config)
    console.log('final', finial_config)
    await RunSuo5WithConfig(finial_config)
  }

  const onFormError = (errors: any) => {
    console.log('表单验证错误:', errors)
    const errorMessages = Object.keys(errors).map(key => {
      const error = errors[key]
      const fieldNames: Record<string, string> = {
        method: '请求方法',
        target: '目标地址',
        listen: '监听地址',
        forward_target: '转发地址',
        template_target: '模板地址'
      }
      return `${fieldNames[key] || key}: ${error.message || '此字段为必填项'}`
    }).join(', ')
    setErrorMessage(`表单验证失败: ${errorMessages}`)
  }

  const onConnectSuccess = (e: string) => {
    setStatus(ConnectStatus.SUCCESS)
    setConnectDisabled(false)
    setConfig(pre => {
      if (!pre) {
        return pre
      }
      let mode = "全双工"
      if (e == "half") {
        mode = "半双工"
      } else if (e == "classic") {
        mode = "短连接"
      } else if (e == "full") {
        mode = "全双工"
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
    setConnectDisabled(false)
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
      // defaultConfig.target = 'http://localhost:8011/tomcat_test_war_exploded/suo5_new.jsp'
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
          <form onSubmit={form.handleSubmit(onConnecting, onFormError)}
                className="flex flex-col gap-y-4 bg-card text-card-foreground border border-border/80 rounded-md shadow p-4">
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

            <div className="flex gap-4">
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
            </div>

            <div className="flex gap-4">
              <FormField
                control={form.control}
                name="mode"
                render={({field}) => (
                  <FormItem className="flex *:h-8 *:text-sm w-full">
                    <FormLabel className="min-w-[36px]">协议</FormLabel>
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
                  <Button disabled={connectDisabled} onClick={onStop}><Loader className="animate-spin"/>连接中</Button>
              }

              {status === ConnectStatus.SUCCESS &&
                  <Button disabled={connectDisabled} variant="default" onClick={onStop}><CircleStop/>停止连接</Button>
              }

              {(status === ConnectStatus.INITIAL || status === ConnectStatus.FAILED) &&
                  <Button disabled={connectDisabled}><Zap/>立即链接</Button>
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