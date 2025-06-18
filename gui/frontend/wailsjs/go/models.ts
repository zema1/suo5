export namespace main {
	
	export class RunStatus {
	    connection_count: number;
	    upload: string;
	    download: string;
	
	    static createFrom(source: any = {}) {
	        return new RunStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.connection_count = source["connection_count"];
	        this.upload = source["upload"];
	        this.download = source["download"];
	    }
	}

}

export namespace suo5 {
	
	export class Suo5Config {
	    method: string;
	    listen: string;
	    target: string;
	    username: string;
	    password: string;
	    mode: string;
	    timeout: number;
	    debug: boolean;
	    upstream_proxy: string[];
	    redirect_url: string;
	    raw_header: string[];
	    disable_heartbeat: boolean;
	    disable_gzip: boolean;
	    enable_cookiejar: boolean;
	    exclude_domain: string[];
	    forward_target: string;
	    max_body_size: number;
	    classic_poll_qps: number;
	    retry_count: number;
	
	    static createFrom(source: any = {}) {
	        return new Suo5Config(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.method = source["method"];
	        this.listen = source["listen"];
	        this.target = source["target"];
	        this.username = source["username"];
	        this.password = source["password"];
	        this.mode = source["mode"];
	        this.timeout = source["timeout"];
	        this.debug = source["debug"];
	        this.upstream_proxy = source["upstream_proxy"];
	        this.redirect_url = source["redirect_url"];
	        this.raw_header = source["raw_header"];
	        this.disable_heartbeat = source["disable_heartbeat"];
	        this.disable_gzip = source["disable_gzip"];
	        this.enable_cookiejar = source["enable_cookiejar"];
	        this.exclude_domain = source["exclude_domain"];
	        this.forward_target = source["forward_target"];
	        this.max_body_size = source["max_body_size"];
	        this.classic_poll_qps = source["classic_poll_qps"];
	        this.retry_count = source["retry_count"];
	    }
	}

}

