export namespace ctrl {
	
	export class Suo5Config {
	    method: string;
	    listen: string;
	    target: string;
	    no_auth: boolean;
	    username: string;
	    password: string;
	    mode: string;
	    buffer_size: number;
	    timeout: number;
	    debug: boolean;
	    upstream_proxy: string;
	    redirect_url: string;
	    raw_header: string[];
	    disable_heartbeat: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Suo5Config(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.method = source["method"];
	        this.listen = source["listen"];
	        this.target = source["target"];
	        this.no_auth = source["no_auth"];
	        this.username = source["username"];
	        this.password = source["password"];
	        this.mode = source["mode"];
	        this.buffer_size = source["buffer_size"];
	        this.timeout = source["timeout"];
	        this.debug = source["debug"];
	        this.upstream_proxy = source["upstream_proxy"];
	        this.redirect_url = source["redirect_url"];
	        this.raw_header = source["raw_header"];
	        this.disable_heartbeat = source["disable_heartbeat"];
	    }
	}

}

export namespace main {
	
	export class Status {
	    connection_count: number;
	    memory_usage: string;
	    cpu_percent: string;
	
	    static createFrom(source: any = {}) {
	        return new Status(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.connection_count = source["connection_count"];
	        this.memory_usage = source["memory_usage"];
	        this.cpu_percent = source["cpu_percent"];
	    }
	}

}

